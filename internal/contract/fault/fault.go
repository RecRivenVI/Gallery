package fault

import "fmt"

// Code 是客户端可以稳定判断的失败语义。文案和内部原因不属于协议。
type Code string

const (
	CodeInternal                 Code = "INTERNAL_ERROR"
	CodeValidation               Code = "VALIDATION_ERROR"
	CodeConfigInvalid            Code = "CONFIG_INVALID"
	CodeUnauthenticated          Code = "UNAUTHENTICATED"
	CodeForbidden                Code = "FORBIDDEN"
	CodeNotFound                 Code = "NOT_FOUND"
	CodeConflict                 Code = "CONFLICT"
	CodeAppDirsOverlap           Code = "APPDIRS_SOURCE_OVERLAP"
	CodeSourceRootsOverlap       Code = "SOURCE_ROOTS_OVERLAP"
	CodeDatabaseOpen             Code = "DATABASE_OPEN_FAILED"
	CodeMigrationFailed          Code = "MIGRATION_FAILED"
	CodeBackupFailed             Code = "BACKUP_FAILED"
	CodeCursorInvalid            Code = "CURSOR_INVALID"
	CodeCursorExpired            Code = "CURSOR_EXPIRED"
	CodeQueryTooShort            Code = "QUERY_TOO_SHORT"
	CodeOverlayFactInvalid       Code = "OVERLAY_FACT_INVALID"
	CodeOverlayProjectionFailed  Code = "OVERLAY_PROJECTION_FAILED"
	CodeBindingReviewRequired    Code = "BINDING_REVIEW_REQUIRED"
	CodeDerivedAssetInvalid      Code = "DERIVED_ASSET_INVALID"
	CodeDerivedAssetFailed       Code = "DERIVED_ASSET_FAILED"
	CodeRuleSchemaInvalid        Code = "RULE_SCHEMA_INVALID"
	CodeRuleCompile              Code = "RULE_COMPILE_ERROR"
	CodeRuleCELLimit             Code = "RULE_CEL_LIMIT"
	CodeRuleDryRun               Code = "RULE_DRY_RUN_FAILED"
	CodeRuleImpact               Code = "RULE_IMPACT_FAILED"
	CodeRuleEval                 Code = "RULE_EVAL_ERROR"
	CodeCatalogPublicationAbsent Code = "CATALOG_PUBLICATION_MISSING"
	CodeContentChangedDuringHash Code = "CONTENT_CHANGED_DURING_HASH"
	CodeMediaOffline             Code = "MEDIA_OFFLINE"
	CodeHostRejected             Code = "HOST_REJECTED"
	CodeOriginRejected           Code = "ORIGIN_REJECTED"
	CodeCSRFInvalid              Code = "CSRF_INVALID"
	CodePairingInvalid           Code = "PAIRING_INVALID"
	CodePairingExpired           Code = "PAIRING_EXPIRED"
	CodeSourcePathInvalid        Code = "SOURCE_PATH_INVALID"
	CodeRuleParameterInvalid     Code = "RULE_PARAMETER_INVALID"
	CodeJobStateConflict         Code = "JOB_STATE_CONFLICT"
	CodeScanAlreadyRunning       Code = "SCAN_ALREADY_RUNNING"
	CodeSourceUnavailable        Code = "SOURCE_UNAVAILABLE"
	CodeSourceReadFailed         Code = "SOURCE_READ_FAILED"
	CodeContentDisappeared       Code = "CONTENT_DISAPPEARED"
	CodePathEscape               Code = "PATH_ESCAPE"
	CodeCatalogCandidateInvalid  Code = "CATALOG_CANDIDATE_INVALID"
	CodeProcessInterrupted       Code = "PROCESS_INTERRUPTED"
	CodeRangeInvalid             Code = "RANGE_INVALID"
	// 以下为进程启动期（bootstrap）失败码，不经 HTTP 暴露，仅用于日志与退出诊断。
	CodeInstanceAlreadyRunning Code = "INSTANCE_ALREADY_RUNNING"
	CodeLockUnavailable        Code = "LOCK_UNAVAILABLE"
)

// Error 在进程内保留 cause，但对外序列化时只应暴露稳定字段。
type Error struct {
	Code      Code
	Retryable bool
	Field     string
	Cause     error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Cause == nil {
		return string(e.Code)
	}
	return fmt.Sprintf("%s: %v", e.Code, e.Cause)
}

func (e *Error) Unwrap() error { return e.Cause }

func New(code Code, retryable bool, cause error) *Error {
	return &Error{Code: code, Retryable: retryable, Cause: cause}
}

func WithField(code Code, field string, cause error) *Error {
	return &Error{Code: code, Field: field, Cause: cause}
}
