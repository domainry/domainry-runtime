package actionmodel

import (
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type ActionResult struct {
	ActionKey          string                             `json:"action_key"`
	ObjectKey          string                             `json:"object_key"`
	RecordID           string                             `json:"record_id"`
	Message            string                             `json:"message"`
	Record             recordmodel.Record                 `json:"record"`
	Output             map[string]any                     `json:"output,omitempty"`
	CreatedRecords     []ActionObjectRecordRef            `json:"created_records,omitempty"`
	UpdatedRecords     []ActionObjectRecordRef            `json:"updated_records,omitempty"`
	DeletedRecords     []ActionObjectRecordRef            `json:"deleted_records,omitempty"`
	RestoredRecords    []ActionObjectRecordRef            `json:"restored_records,omitempty"`
	TriggeredWorkflows []workflowmodel.WorkflowRunSummary `json:"triggered_workflows"`
	NoStore            bool                               `json:"-"`
}

type ActionObjectRequest struct {
	Data                 map[string]any `json:"data,omitempty"`
	TargetOrganizationID string         `json:"target_organization_id,omitempty"`
	AssuranceToken       string         `json:"assurance_token,omitempty"`
}

type ActionObjectRecordRef struct {
	ObjectKey string `json:"object_key"`
	RecordID  string `json:"record_id"`
}

type ActionObjectResult struct {
	ActionKey          string                             `json:"action_key"`
	ObjectKey          string                             `json:"object_key"`
	Status             string                             `json:"status"`
	Message            string                             `json:"message,omitempty"`
	Output             map[string]any                     `json:"output,omitempty"`
	CreatedRecords     []ActionObjectRecordRef            `json:"created_records,omitempty"`
	UpdatedRecords     []ActionObjectRecordRef            `json:"updated_records,omitempty"`
	DeletedRecords     []ActionObjectRecordRef            `json:"deleted_records,omitempty"`
	RestoredRecords    []ActionObjectRecordRef            `json:"restored_records,omitempty"`
	TriggeredWorkflows []workflowmodel.WorkflowRunSummary `json:"triggered_workflows,omitempty"`
	NoStore            bool                               `json:"-"`
}

type ActionBulkRequest struct {
	RecordIDs        []string       `json:"record_ids"`
	Data             map[string]any `json:"data,omitempty"`
	ExpectedVersions map[string]int `json:"expected_versions,omitempty"`
	IdempotencyKey   string         `json:"-"`
}

type ActionBulkItemResult struct {
	RecordID string        `json:"record_id"`
	Success  bool          `json:"success"`
	Result   *ActionResult `json:"result,omitempty"`
	Code     string        `json:"code,omitempty"`
	Error    string        `json:"error,omitempty"`
}

type ActionBulkResult struct {
	ActionKey string                 `json:"action_key"`
	ObjectKey string                 `json:"object_key"`
	Total     int                    `json:"total"`
	Succeeded int                    `json:"succeeded"`
	Failed    int                    `json:"failed"`
	Message   string                 `json:"message"`
	Items     []ActionBulkItemResult `json:"items"`
}
