package legacy

import (
	"bytes"
	"encoding/json"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// unknownFieldReason 是「旧配置声明了本转换器完全不认识的字段」这一类登记的统一原因。
//
// 它与其它未转换登记的区别在于：其它登记说明「这个语义我们理解、但当前无法表达」，本类登记说明
// 「这个字段我们根本没看见」。两者都必须出现在结果里，但后者是更危险的一类——`Config` 是手写的
// 部分映射，`encoding/json` 对未声明字段一律静默丢弃，没有任何返回值、错误或日志能暴露它。
const unknownFieldReason = "旧配置声明了本转换器未识别的字段；它不进入任何规则原语，登记于此以免静默消失"

// rawMessageType 是 json.RawMessage 的反射类型。声明为 json.RawMessage 的字段表示「这段原文由
// 手写逻辑单独处理」，因此差集比对到此为止，不再往下展开。
var rawMessageType = reflect.TypeOf(json.RawMessage(nil))

// collectUnknownFields 把旧配置原文与 Config 的**已声明字段集合**做差集，逐项登记差集。
//
// 存在的理由：Config 只映射本转换器实际使用的字段，未声明的字段会被 `encoding/json` 静默丢弃。
// 「转换成功」因此不能证明「旧配置的每个字段都被处理过」——它只证明被声明的那些字段解析成功。
// 本函数用原始 JSON 树与 Config 的反射类型逐层比对，把差集变成可核对的登记。
//
// 覆盖范围是**逐层递归**的，不只有顶层：只要某个字段的父路径被声明，它的未声明兄弟就会被登记。
// 两处刻意的边界：
//   - 声明为 json.RawMessage 的字段（封面候选条件、角标 metadata 取值）是有意保留的原文，其内部
//     结构由手写逻辑解释，因此不展开；这些位置在覆盖矩阵中另行逐项判定。
//   - 未声明的对象只登记它自己这一条，不再逐个叶子展开——「整棵子树未被识别」是一条事实，
//     拆成十条不会增加信息。
func collectUnknownFields(input []byte) ([]Note, error) {
	var document any
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.UseNumber()
	if err := decoder.Decode(&document); err != nil {
		return nil, err
	}
	notes := []Note{}
	walkDeclared(document, reflect.TypeOf(Config{}), "", "", &notes)
	return notes, nil
}

// walkDeclared 沿「JSON 原文 × 已声明类型」两棵树同步下行，把只出现在原文一侧的键登记为未转换。
func walkDeclared(value any, declared reflect.Type, pointer, platformID string, notes *[]Note) {
	for declared != nil && declared.Kind() == reflect.Pointer {
		declared = declared.Elem()
	}
	if declared == nil || declared == rawMessageType {
		return
	}
	switch typed := value.(type) {
	case map[string]any:
		switch declared.Kind() {
		case reflect.Struct:
			fields := declaredFields(declared)
			for _, key := range sortedObjectKeys(typed) {
				child := pointer + "/" + escapeJSONPointer(key)
				fieldType, ok := fields[key]
				if !ok {
					*notes = append(*notes, Note{Platform: platformID, Field: child, Reason: unknownFieldReason})
					continue
				}
				walkDeclared(typed[key], fieldType, child, platformID, notes)
			}
		case reflect.Map:
			// map 的键集合本身是开放的（例如角标的 metadata 条件），键不构成「未声明」，
			// 但值仍要按声明的元素类型继续比对。
			for _, key := range sortedObjectKeys(typed) {
				walkDeclared(typed[key], declared.Elem(), pointer+"/"+escapeJSONPointer(key), platformID, notes)
			}
		}
	case []any:
		if declared.Kind() != reflect.Slice && declared.Kind() != reflect.Array {
			return
		}
		for index, item := range typed {
			// 平台是 Note.Platform 的来源：登记落在哪个平台上，比只给一个数组下标可核对得多。
			itemPlatform := platformID
			if pointer == "/platforms" {
				itemPlatform = platformIdentifier(item)
			}
			walkDeclared(item, declared.Elem(), pointer+"/"+strconv.Itoa(index), itemPlatform, notes)
		}
	}
}

// declaredFields 按 json tag 建立「JSON 键 → 字段类型」表。没有 tag 时用字段名，与
// encoding/json 的默认行为一致；`json:"-"` 表示该字段不参与 JSON，因此也不算已声明。
func declaredFields(declared reflect.Type) map[string]reflect.Type {
	result := make(map[string]reflect.Type, declared.NumField())
	for index := 0; index < declared.NumField(); index++ {
		field := declared.Field(index)
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "-" {
			continue
		}
		if name == "" {
			name = field.Name
		}
		result[name] = field.Type
	}
	return result
}

func platformIdentifier(value any) string {
	object, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	identifier, _ := object["id"].(string)
	return identifier
}

func sortedObjectKeys(object map[string]any) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// escapeJSONPointer 按 RFC 6901 转义 JSON Pointer 的引用记号，使登记的路径可以直接用于定位。
func escapeJSONPointer(token string) string {
	return strings.ReplaceAll(strings.ReplaceAll(token, "~", "~0"), "/", "~1")
}
