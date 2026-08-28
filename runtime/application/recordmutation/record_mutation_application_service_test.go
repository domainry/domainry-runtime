package recordmutation

import (
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"context"
	"errors"
	"testing"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type mutationOperationsProbe struct {
	operation string
	objectKey string
	recordID  string
	data      map[string]any
}

func (p *mutationOperationsProbe) CreateRecord(_ context.Context, objectKey string, data map[string]any, _ principalmodel.Principal) (recordmodel.Record, error) {
	p.operation, p.objectKey, p.data = "create", objectKey, data
	return recordmodel.Record{ID: "created"}, nil
}

func (p *mutationOperationsProbe) UpdateRecord(_ context.Context, objectKey, recordID string, data map[string]any, _ principalmodel.Principal) (recordmodel.Record, error) {
	p.operation, p.objectKey, p.recordID, p.data = "update", objectKey, recordID, data
	return recordmodel.Record{ID: recordID}, nil
}

func (p *mutationOperationsProbe) DeleteRecord(_ context.Context, objectKey, recordID string, _ principalmodel.Principal) error {
	p.operation, p.objectKey, p.recordID = "delete", objectKey, recordID
	return nil
}

func (p *mutationOperationsProbe) RestoreRecord(_ context.Context, objectKey, recordID string, _ principalmodel.Principal) (recordmodel.Record, error) {
	p.operation, p.objectKey, p.recordID = "restore", objectKey, recordID
	return recordmodel.Record{ID: recordID}, nil
}

func TestMutationDispatcherOwnsCrossDomainWriteRouting(t *testing.T) {
	operations := &mutationOperationsProbe{}
	dispatcher := NewRecordMutationApplicationService(operations)
	data := map[string]any{"stage": "won"}

	record, err := dispatcher.Dispatch(t.Context(), RecordMutationRequest{Operation: RecordMutationTransition, ObjectKey: " opportunity ", RecordID: " op-1 ", Data: data})
	if err != nil {
		t.Fatal(err)
	}
	if record.ID != "op-1" || operations.operation != "update" || operations.objectKey != "opportunity" || operations.recordID != "op-1" || operations.data["stage"] != "won" {
		t.Fatalf("record=%#v operations=%#v", record, operations)
	}
	record, err = dispatcher.Dispatch(t.Context(), RecordMutationRequest{Operation: RecordMutationCreate, ObjectKey: " customer ", Data: map[string]any{"name": "Acme"}})
	if err != nil || record.ID != "created" || operations.operation != "create" || operations.objectKey != "customer" || operations.data["name"] != "Acme" {
		t.Fatalf("create record=%#v operations=%#v err=%v", record, operations, err)
	}

	_, err = dispatcher.Dispatch(t.Context(), RecordMutationRequest{Operation: RecordMutationDelete, ObjectKey: "customer", RecordID: "c1"})
	if err != nil || operations.operation != "delete" {
		t.Fatalf("delete operation=%#v err=%v", operations, err)
	}
	record, err = dispatcher.Dispatch(t.Context(), RecordMutationRequest{Operation: RecordMutationRestore, ObjectKey: " customer ", RecordID: " c1 "})
	if err != nil || record.ID != "c1" || operations.operation != "restore" || operations.objectKey != "customer" || operations.recordID != "c1" {
		t.Fatalf("restore record=%#v operations=%#v err=%v", record, operations, err)
	}
}

func TestMutationDispatcherRejectsUnknownOperation(t *testing.T) {
	dispatcher := NewRecordMutationApplicationService(&mutationOperationsProbe{})
	_, err := dispatcher.Dispatch(t.Context(), RecordMutationRequest{Operation: RecordMutationOperation("merge")})
	var appErr *apperror.AppError
	if !errors.As(err, &appErr) || appErr.Kind != apperror.KindBadRequest || appErr.Code != "backend.record.mutation_operation_invalid" || appErr.Params["operation"] != "merge" {
		t.Fatalf("error = %#v", err)
	}
}
