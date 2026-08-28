package projection

import (
	"fmt"
	"strings"

	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func WorkflowExecutionMatchesRecordFilter(execution workflowmodel.WorkflowExecution, objectKey, recordID string) bool {
	if workflowExecutionObjectRecordMatch(execution.ObjectKey, execution.RecordID, objectKey, recordID) {
		return true
	}
	if workflowExecutionObjectRecordMatch(workflowMapStringValue(execution.Payload, "object_key"), workflowMapStringValue(execution.Payload, "record_id"), objectKey, recordID) {
		return true
	}
	for _, pair := range [][2]string{{"trigger_object_key", "trigger_record_id"}, {"created_object_key", "created_record_id"}, {"updated_object_key", "updated_record_id"}} {
		if workflowExecutionObjectRecordMatch(workflowMapStringValue(execution.Result, pair[0]), workflowMapStringValue(execution.Result, pair[1]), objectKey, recordID) {
			return true
		}
	}
	return false
}

func WorkflowExecutionRecordIDsForObject(execution workflowmodel.WorkflowExecution, objectKey string) []string {
	seen := map[string]bool{}
	ids := []string{}
	add := func(candidateObjectKey, candidateRecordID string) {
		candidateObjectKey = strings.TrimSpace(candidateObjectKey)
		candidateRecordID = strings.TrimSpace(candidateRecordID)
		if candidateObjectKey != objectKey || candidateRecordID == "" || seen[candidateRecordID] {
			return
		}
		seen[candidateRecordID] = true
		ids = append(ids, candidateRecordID)
	}
	add(execution.ObjectKey, execution.RecordID)
	add(workflowMapStringValue(execution.Payload, "object_key"), workflowMapStringValue(execution.Payload, "record_id"))
	for _, pair := range [][2]string{{"trigger_object_key", "trigger_record_id"}, {"created_object_key", "created_record_id"}, {"updated_object_key", "updated_record_id"}} {
		add(workflowMapStringValue(execution.Result, pair[0]), workflowMapStringValue(execution.Result, pair[1]))
	}
	return ids
}

func workflowExecutionObjectRecordMatch(candidateObjectKey, candidateRecordID, objectKey, recordID string) bool {
	if objectKey != "" && candidateObjectKey != objectKey {
		return false
	}
	if recordID != "" && candidateRecordID != recordID {
		return false
	}
	return candidateObjectKey != "" || candidateRecordID != ""
}

func workflowMapStringValue(values map[string]any, key string) string {
	if values == nil || values[key] == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(values[key]))
}
