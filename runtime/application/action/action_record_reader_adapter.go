package action

import (
	"context"

	actioncontract "github.com/domainry/domainry-runtime/runtime/domain/action/contract"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
)

type ActionRecordReaderAdapter struct {
	repository recordrepository.RecordRepository
}

var _ actioncontract.ActionRecordReader = (*ActionRecordReaderAdapter)(nil)

func NewActionRecordReaderAdapter(repository recordrepository.RecordRepository) *ActionRecordReaderAdapter {
	return &ActionRecordReaderAdapter{repository: repository}
}

func (a *ActionRecordReaderAdapter) GetActionRecord(ctx context.Context, workspaceID string, object definitionmodel.ObjectSchema, recordID string) (recordmodel.Record, bool, error) {
	if a == nil || a.repository == nil {
		return recordmodel.Record{}, false, nil
	}
	return a.repository.GetRecord(ctx, workspaceID, object, recordID)
}

func (a *ActionRecordReaderAdapter) ListActionRecords(ctx context.Context, workspaceID string, object definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	if a == nil || a.repository == nil {
		return recordmodel.RecordPageResult{}, nil
	}
	return a.repository.ListRecords(ctx, workspaceID, object, query)
}
