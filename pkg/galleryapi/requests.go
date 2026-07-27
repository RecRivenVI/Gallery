package galleryapi

// NewDirectSourceRuleBindingCreateRequest 构造直接冻结 RuleVersion 与参数的互斥请求模式。
func NewDirectSourceRuleBindingCreateRequest(sourceID SourceId, semanticHash SHA256Digest, parameters map[string]interface{}, priority int) SourceRuleBindingCreateRequest {
	return SourceRuleBindingCreateRequest{
		SourceId: sourceID, SemanticHash: &semanticHash, Parameters: &parameters, Priority: priority,
	}
}

// NewParameterSourceRuleBindingCreateRequest 构造引用持久参数集的互斥请求模式。
func NewParameterSourceRuleBindingCreateRequest(sourceID SourceId, parameterID RuleParameterId, priority int, override, condition map[string]interface{}) SourceRuleBindingCreateRequest {
	result := SourceRuleBindingCreateRequest{SourceId: sourceID, ParameterId: &parameterID, Priority: priority}
	if override != nil {
		result.Override = &override
	}
	if condition != nil {
		result.Condition = &condition
	}
	return result
}
