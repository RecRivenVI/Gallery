package rules

import (
	"context"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	exprpb "google.golang.org/genproto/googleapis/api/expr/v1alpha1"
)

var reflectAnyType = reflect.TypeOf((*any)(nil)).Elem()
var celRegexLiteralPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\.matches\("([^"\\]*(?:\\.[^"\\]*)*)"\)`),
	regexp.MustCompile(`\.matches\('([^'\\]*(?:\\.[^'\\]*)*)'\)`),
}

type celRuntime struct {
	environment *cel.Env
	programs    sync.Map
}

type celEvaluation struct {
	Value    any
	Cost     uint64
	Duration time.Duration
}

func newCELRuntime() (*celRuntime, error) {
	environment, err := cel.NewEnv(
		cel.Variable("source", cel.DynType),
		cel.Variable("path", cel.DynType),
		cel.Variable("file", cel.DynType),
		cel.Variable("metadata", cel.DynType),
		cel.Variable("candidate", cel.DynType),
		cel.Variable("params", cel.DynType),
	)
	if err != nil {
		return nil, err
	}
	return &celRuntime{environment: environment}, nil
}

func validateCELExpressions(expressions []IRExpression) error {
	runtime, err := newCELRuntime()
	if err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(expressions))
	for index, expression := range expressions {
		if _, exists := seen[expression.ID]; exists {
			return fmt.Errorf("cel_expressions/%d: expression id 重复", index)
		}
		seen[expression.ID] = struct{}{}
		if len(expression.Expression) > CELProfileV1.ExpressionBytes {
			return fmt.Errorf("cel_expressions/%d/expression: CEL_EXPRESSION_TOO_LONG", index)
		}
		for _, pattern := range celRegexLiteralPatterns {
			for _, match := range pattern.FindAllStringSubmatch(expression.Expression, -1) {
				if len([]rune(match[1])) > CELProfileV1.RegexCharacters {
					return withField(fmt.Sprintf("/cel_expressions/%d/expression", index), fmt.Errorf("CEL_REGEX_LIMIT"))
				}
			}
		}
		ast, issues := runtime.environment.Compile(expression.Expression)
		if issues != nil && issues.Err() != nil {
			return fmt.Errorf("cel_expressions/%d/expression: %w", index, issues.Err())
		}
		if nodes := countExprNodes(ast.Expr()); nodes > CELProfileV1.ASTNodes {
			return fmt.Errorf("cel_expressions/%d/expression: CEL_AST_LIMIT", index)
		}
		if expression.Purpose == "predicate" && ast.OutputType() != cel.BoolType && ast.OutputType() != cel.DynType {
			return fmt.Errorf("cel_expressions/%d/purpose: predicate 必须返回 bool", index)
		}
	}
	return nil
}

func (r *celRuntime) evaluate(ctx context.Context, expression IRExpression, activation map[string]any) (celEvaluation, error) {
	programValue, ok := r.programs.Load(expression.ID + "\x00" + expression.Expression)
	if !ok {
		ast, issues := r.environment.Compile(expression.Expression)
		if issues != nil && issues.Err() != nil {
			return celEvaluation{}, issues.Err()
		}
		program, err := r.environment.Program(ast, cel.CostTracking(nil), cel.CostLimit(uint64(CELProfileV1.Cost)), cel.InterruptCheckFrequency(100))
		if err != nil {
			return celEvaluation{}, err
		}
		programValue, _ = r.programs.LoadOrStore(expression.ID+"\x00"+expression.Expression, program)
	}
	program := programValue.(cel.Program)
	deadline, cancel := context.WithTimeout(ctx, time.Duration(CELProfileV1.ExecutionMillis)*time.Millisecond)
	defer cancel()
	started := time.Now()
	value, details, err := program.ContextEval(deadline, activation)
	duration := time.Since(started)
	if err != nil {
		if strings.Contains(err.Error(), "operation cancelled") || deadline.Err() != nil {
			return celEvaluation{}, fmt.Errorf("CEL_EXECUTION_LIMIT: %w", err)
		}
		return celEvaluation{}, err
	}
	var cost uint64
	if details != nil && details.ActualCost() != nil {
		cost = *details.ActualCost()
	}
	native, err := celNative(value)
	if err != nil {
		return celEvaluation{}, err
	}
	return celEvaluation{Value: native, Cost: cost, Duration: duration}, nil
}

func celNative(value ref.Val) (any, error) {
	if value == types.NullValue {
		return nil, nil
	}
	native, err := value.ConvertToNative(reflectAnyType)
	if err == nil {
		return native, nil
	}
	return value.Value(), nil
}

func countExprNodes(expression *exprpb.Expr) int {
	if expression == nil {
		return 0
	}
	count := 1
	switch kind := expression.ExprKind.(type) {
	case *exprpb.Expr_SelectExpr:
		count += countExprNodes(kind.SelectExpr.Operand)
	case *exprpb.Expr_CallExpr:
		count += countExprNodes(kind.CallExpr.Target)
		for _, argument := range kind.CallExpr.Args {
			count += countExprNodes(argument)
		}
	case *exprpb.Expr_ListExpr:
		for _, element := range kind.ListExpr.Elements {
			count += countExprNodes(element)
		}
	case *exprpb.Expr_StructExpr:
		for _, entry := range kind.StructExpr.Entries {
			count += countExprNodes(entry.GetMapKey()) + countExprNodes(entry.GetValue())
		}
	case *exprpb.Expr_ComprehensionExpr:
		value := kind.ComprehensionExpr
		count += countExprNodes(value.IterRange) + countExprNodes(value.AccuInit) + countExprNodes(value.LoopCondition) + countExprNodes(value.LoopStep) + countExprNodes(value.Result)
	}
	return count
}
