package service

import (
	"context"
	"errors"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestDeleteRelationApplyRemainingErrorsAndOptionalCallbacks(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "order", Fields: []definitionmodel.FieldSchema{
		{Key: "name", Type: "text"},
		{Key: "other", Type: "relation", Config: map[string]any{"target": "other"}},
		{Key: "customer", Type: "relation", Config: map[string]any{"target": "customer", "on_delete": "set_null"}},
		{Key: "customer_cascade", Type: "relation", Config: map[string]any{"target": "customer", "on_delete": "cascade"}},
	}}
	serviceFor := func(repository *deleteRelationRepositoryProbe) *RecordDeleteRelationDomainService {
		return NewRecordDeleteRelationDomainService(repository, func() map[string]definitionmodel.ObjectSchema {
			return map[string]definitionmodel.ObjectSchema{"order": object}
		})
	}
	if err := serviceFor(&deleteRelationRepositoryProbe{err: errors.New("store")}).Apply(t.Context(), "workspace", "customer", "customer-1", RecordDeleteRelationCallbacks{}); err == nil {
		t.Fatal("reference scan error was ignored")
	}
	repository := &deleteRelationRepositoryProbe{pages: map[string][]recordmodel.RecordPageResult{"order": {{Items: []recordmodel.Record{{ID: "order-1"}}}}}}
	if err := serviceFor(repository).Apply(t.Context(), "workspace", "customer", "customer-1", RecordDeleteRelationCallbacks{}); err != nil {
		t.Fatalf("nil callbacks failed: %v", err)
	}
	callbackErr := errors.New("callback failed")
	if err := serviceFor(repository).Apply(t.Context(), "workspace", "customer", "customer-1", RecordDeleteRelationCallbacks{SetNull: func(context.Context, RecordDeleteReference) error { return callbackErr }}); !errors.Is(err, callbackErr) {
		t.Fatalf("set-null error=%v", err)
	}
	if err := serviceFor(repository).Apply(t.Context(), "workspace", "customer", "customer-1", RecordDeleteRelationCallbacks{Cascade: func(context.Context, RecordDeleteReference) error { return callbackErr }}); !errors.Is(err, callbackErr) {
		t.Fatalf("cascade error=%v", err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := serviceFor(repository).Apply(cancelled, "workspace", "customer", "customer-1", RecordDeleteRelationCallbacks{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error=%v", err)
	}
}
