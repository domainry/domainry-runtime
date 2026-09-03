package workflow

import (
	"context"
	"fmt"
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
)

type WorkflowRecordReaderAdapter struct {
	repository recordrepository.RecordRepository
	normalize  func(definitionmodel.ObjectSchema, recordmodel.RecordListQuery, principalmodel.Principal) recordmodel.RecordListQuery
}

func NewWorkflowRecordReaderAdapter(repository recordrepository.RecordRepository, normalize func(definitionmodel.ObjectSchema, recordmodel.RecordListQuery, principalmodel.Principal) recordmodel.RecordListQuery) WorkflowRecordReaderAdapter {
	return WorkflowRecordReaderAdapter{repository: repository, normalize: normalize}
}

func (a WorkflowRecordReaderAdapter) GetWorkflowRecord(ctx context.Context, workspaceID string, object definitionmodel.ObjectSchema, recordID string, principal principalmodel.Principal) (recordmodel.Record, bool, error) {
	if a.repository == nil {
		return recordmodel.Record{}, false, nil
	}
	if strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(workspaceID) != strings.TrimSpace(principal.WorkspaceID) {
		return recordmodel.Record{}, false, fmt.Errorf("workflow record workspace does not match authorized principal")
	}
	if a.normalize == nil {
		return recordmodel.Record{}, false, fmt.Errorf("workflow record scope normalizer is unavailable")
	}
	query := a.normalize(object, recordmodel.RecordListQuery{Page: 1, PageSize: 1, SkipTotal: true, Filters: map[string]any{"id__in": []any{recordID}}}, principal)
	page, err := a.repository.ListRecords(ctx, workspaceID, object, query)
	if err != nil || len(page.Items) == 0 {
		return recordmodel.Record{}, false, err
	}
	return page.Items[0], true, nil
}

func (a WorkflowRecordReaderAdapter) ListWorkflowRecords(ctx context.Context, workspaceID string, object definitionmodel.ObjectSchema, query recordmodel.RecordListQuery, principal principalmodel.Principal) (recordmodel.RecordPageResult, error) {
	if a.repository == nil {
		return recordmodel.RecordPageResult{}, nil
	}
	if strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(workspaceID) != strings.TrimSpace(principal.WorkspaceID) {
		return recordmodel.RecordPageResult{}, fmt.Errorf("workflow record workspace does not match authorized principal")
	}
	if a.normalize == nil {
		return recordmodel.RecordPageResult{}, fmt.Errorf("workflow record scope normalizer is unavailable")
	}
	query = a.normalize(object, query, principal)
	return a.repository.ListRecords(ctx, workspaceID, object, query)
}
