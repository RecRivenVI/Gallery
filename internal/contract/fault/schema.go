package fault

import (
	_ "embed"

	contractschema "github.com/RecRivenVI/gallery/internal/contract/schema"
)

//go:embed error.schema.json
var errorSchema []byte

func AllCodes() []Code {
	return []Code{
		CodeInternal, CodeValidation, CodeConfigInvalid, CodeUnauthenticated, CodeForbidden,
		CodeNotFound, CodeConflict, CodeAppDirsOverlap, CodeSourceRootsOverlap, CodeDatabaseOpen,
		CodeMigrationFailed, CodeBackupFailed, CodeCursorInvalid, CodeCursorExpired,
		CodeRuleSchemaInvalid, CodeRuleEval, CodeCatalogPublicationAbsent,
		CodeDerivedAssetInvalid, CodeDerivedAssetUnavailable, CodeDerivedAssetFailed,
		CodeExternalToolUnavailable, CodeExternalToolFailed,
		CodeContentChangedDuringHash, CodeMediaOffline, CodeContentNotVerified, CodeHostRejected, CodeOriginRejected,
		CodeCSRFInvalid, CodePairingInvalid, CodePairingExpired, CodeSourcePathInvalid,
		CodeRuleParameterInvalid, CodeRuleDraftConflict, CodeRulePackageConflict,
		CodeRuleParameterConflict, CodeRulePublishBlocked, CodeRuleRollbackBlocked,
		CodeRuleVersionInUse, CodeRuleImportInvalid, CodeRuleBindingConflict, CodeJobStateConflict, CodeScanAlreadyRunning,
		CodeSourceUnavailable, CodeSourceReadFailed, CodeContentDisappeared, CodePathEscape,
		CodeCatalogCandidateInvalid, CodeProcessInterrupted, CodeRangeInvalid,
		CodeRestoreFailed, CodeBackupNotFound, CodeBackupCorrupt, CodeBackupIncompatible,
	}
}

func ErrorSchema() []byte { return append([]byte(nil), errorSchema...) }

func NewErrorValidator() (*contractschema.Validator, error) {
	return contractschema.Compile("error.schema.json", errorSchema)
}
