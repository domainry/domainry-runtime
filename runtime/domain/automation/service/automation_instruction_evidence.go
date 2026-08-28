package service

import (
	automationprojection "github.com/domainry/domainry-runtime/runtime/domain/automation/projection"
	recordcontract "github.com/domainry/domainry-runtime/runtime/domain/record/contract"

	"encoding/json"
	"time"
)

func FailedNodeTrace(nodeID, kind string, started time.Time, err error, invocationID string, input, output map[string]any) automationprojection.AutomationNodeTrace {
	code, params := automationErrorDetails(err)
	return automationprojection.AutomationNodeTrace{
		NodeID:       nodeID,
		Kind:         kind,
		Status:       "failed",
		DurationMS:   time.Since(started).Milliseconds(),
		ErrorCode:    code,
		ErrorReason:  err.Error(),
		ErrorParams:  params,
		InvocationID: invocationID,
		Input:        recordcontract.RecordCloneData(input),
		Output:       recordcontract.RecordCloneData(output),
	}
}

func TraceMap(trace automationprojection.AutomationRuleTrace) map[string]any {
	raw, err := json.Marshal(trace)
	if err != nil {
		return traceFallback(trace)
	}
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return out
}

func traceFallback(trace automationprojection.AutomationRuleTrace) map[string]any {
	return map[string]any{"execution_id": trace.ExecutionID, "rule_key": trace.RuleKey, "status": trace.Status}
}
