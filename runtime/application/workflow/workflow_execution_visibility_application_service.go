package workflow

import (
	"context"
	"fmt"
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	workflowpolicy "github.com/domainry/domainry-runtime/runtime/domain/workflow/policy"
	workflowprojection "github.com/domainry/domainry-runtime/runtime/domain/workflow/projection"
)

var workflowCloneActionOutput = workflowpolicy.WorkflowCloneMap

type WorkflowExecutionVisibilityApplicationService struct {
	recordReader WorkflowRecordReader
	canAccess    func(context.Context, principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool
}

func NewWorkflowExecutionVisibilityApplicationService(recordReader WorkflowRecordReader, canAccess func(context.Context, principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool) *WorkflowExecutionVisibilityApplicationService {
	return &WorkflowExecutionVisibilityApplicationService{recordReader: recordReader, canAccess: canAccess}
}

func (s *WorkflowExecutionVisibilityApplicationService) WorkflowExecutionVisible(ctx context.Context, principal principalmodel.Principal, object definitionmodel.ObjectSchema, execution workflowmodel.WorkflowExecution) (bool, error) {
	if err := workflowAuthorizeQuery(principal); err != nil {
		return false, err
	}
	recordIDs := workflowprojection.WorkflowExecutionRecordIDsForObject(execution, object.Key)
	if len(recordIDs) == 0 {
		return true, nil
	}
	for start := 0; start < len(recordIDs); start += 200 {
		end := min(start+200, len(recordIDs))
		ids := make([]any, 0, end-start)
		for _, recordID := range recordIDs[start:end] {
			if recordID = strings.TrimSpace(recordID); recordID != "" {
				ids = append(ids, recordID)
			}
		}
		if len(ids) == 0 {
			continue
		}
		page, err := s.recordReader.ListWorkflowRecords(ctx, principal.WorkspaceID, object, recordmodel.RecordListQuery{Page: 1, PageSize: len(ids), SkipTotal: true, Filters: map[string]any{"id__in": ids}})
		if err != nil {
			return false, fmt.Errorf("get Workflow execution Record: %w", err)
		}
		for _, record := range page.Items {
			if s.canAccess(ctx, principal, object, record) {
				return true, nil
			}
		}
	}
	return false, nil
}
