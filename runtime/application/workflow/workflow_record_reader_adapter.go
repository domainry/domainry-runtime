package workflow

import (
	"context"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
)

type WorkflowRecordReaderAdapter struct {
	repository recordrepository.RecordRepository
}

func NewWorkflowRecordReaderAdapter(repository recordrepository.RecordRepository) WorkflowRecordReaderAdapter {
	return WorkflowRecordReaderAdapter{repository: repository}
}

func (a WorkflowRecordReaderAdapter) GetWorkflowRecord(ctx context.Context, workspaceID string, object definitionmodel.ObjectSchema, recordID string) (recordmodel.Record, bool, error) {
	if a.repository == nil {
		return recordmodel.Record{}, false, nil
	}
	return a.repository.GetRecord(ctx, workspaceID, object, recordID)
}

func (a WorkflowRecordReaderAdapter) ListWorkflowRecords(ctx context.Context, workspaceID string, object definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	if a.repository == nil {
		return recordmodel.RecordPageResult{}, nil
	}
	return a.repository.ListRecords(ctx, workspaceID, object, query)
}
