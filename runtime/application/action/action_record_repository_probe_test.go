package action

import (
	"context"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

// actionRecordRepositoryProbe is shared by Action tests that need the Record
// repository contract without reintroducing the deleted unscoped reader seam.
type actionRecordRepositoryProbe struct {
	get         func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error)
	list        func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error)
	commitBatch func(context.Context, string, []transactionmodel.RecordMutationCommit) error
}

func (p *actionRecordRepositoryProbe) GetRecord(ctx context.Context, workspaceID string, object definitionmodel.ObjectSchema, recordID string) (recordmodel.Record, bool, error) {
	return p.get(ctx, workspaceID, object, recordID)
}

func (p *actionRecordRepositoryProbe) ListRecords(ctx context.Context, workspaceID string, object definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	return p.list(ctx, workspaceID, object, query)
}

func (*actionRecordRepositoryProbe) InsertRecord(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record) error {
	return nil
}

func (*actionRecordRepositoryProbe) UpdateRecord(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record) error {
	return nil
}

func (*actionRecordRepositoryProbe) UpdateRecordWhere(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, map[string]any) (bool, error) {
	return false, nil
}

func (*actionRecordRepositoryProbe) DeleteRecord(context.Context, string, definitionmodel.ObjectSchema, string) error {
	return nil
}

func (*actionRecordRepositoryProbe) CommitRecordMutation(context.Context, string, transactionmodel.RecordMutationCommit) error {
	return nil
}

func (p *actionRecordRepositoryProbe) CommitRecordMutationBatch(ctx context.Context, workspaceID string, commits []transactionmodel.RecordMutationCommit) error {
	if p.commitBatch != nil {
		return p.commitBatch(ctx, workspaceID, commits)
	}
	return nil
}

func (*actionRecordRepositoryProbe) UniqueExists(context.Context, string, string, string, string, any) (bool, error) {
	return false, nil
}
