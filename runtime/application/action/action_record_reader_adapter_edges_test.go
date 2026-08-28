package action

import (
	"context"
	"errors"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

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

func TestActionRecordReaderAdapterNilAndForwardingBoundaries(t *testing.T) {
	var nilAdapter *ActionRecordReaderAdapter
	if record, found, err := nilAdapter.GetActionRecord(t.Context(), "workspace-a", definitionmodel.ObjectSchema{Key: "order"}, "record-1"); err != nil || found || record.ID != "" {
		t.Fatalf("nil adapter get=%+v/%v/%v", record, found, err)
	}
	if page, err := nilAdapter.ListActionRecords(t.Context(), "workspace-a", definitionmodel.ObjectSchema{Key: "order"}, recordmodel.RecordListQuery{}); err != nil || len(page.Items) != 0 {
		t.Fatalf("nil adapter list=%+v/%v", page, err)
	}
	empty := NewActionRecordReaderAdapter(nil)
	if _, found, err := empty.GetActionRecord(t.Context(), "workspace-a", definitionmodel.ObjectSchema{}, "record-1"); err != nil || found {
		t.Fatalf("nil repository get found=%v error=%v", found, err)
	}
	if page, err := empty.ListActionRecords(t.Context(), "workspace-a", definitionmodel.ObjectSchema{}, recordmodel.RecordListQuery{}); err != nil || len(page.Items) != 0 {
		t.Fatalf("nil repository list=%+v error=%v", page, err)
	}

	want := errors.New("repository failed")
	getCalls, listCalls := 0, 0
	repository := &actionRecordRepositoryProbe{
		get: func(_ context.Context, workspaceID string, object definitionmodel.ObjectSchema, recordID string) (recordmodel.Record, bool, error) {
			getCalls++
			if workspaceID != "workspace-a" || object.Key != "order" || recordID != "record-1" {
				t.Fatalf("get input=%s/%s/%s", workspaceID, object.Key, recordID)
			}
			return recordmodel.Record{ID: recordID}, true, want
		},
		list: func(_ context.Context, workspaceID string, object definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
			listCalls++
			if workspaceID != "workspace-a" || object.Key != "order" || query.Page != 3 {
				t.Fatalf("list input=%s/%s/%+v", workspaceID, object.Key, query)
			}
			return recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "record-1"}}}, want
		},
	}
	adapter := NewActionRecordReaderAdapter(repository)
	if record, found, err := adapter.GetActionRecord(t.Context(), "workspace-a", definitionmodel.ObjectSchema{Key: "order"}, "record-1"); record.ID != "record-1" || !found || !errors.Is(err, want) || getCalls != 1 {
		t.Fatalf("forwarded get=%+v/%v/%v calls=%d", record, found, err, getCalls)
	}
	if page, err := adapter.ListActionRecords(t.Context(), "workspace-a", definitionmodel.ObjectSchema{Key: "order"}, recordmodel.RecordListQuery{Page: 3}); len(page.Items) != 1 || !errors.Is(err, want) || listCalls != 1 {
		t.Fatalf("forwarded list=%+v/%v calls=%d", page, err, listCalls)
	}
}
