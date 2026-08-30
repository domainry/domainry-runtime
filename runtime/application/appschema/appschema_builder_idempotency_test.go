package appschema

import (
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
)

func TestMetadataBuilderIdempotencyRejectsSameKeyWithDifferentPayload(t *testing.T) {
	before := appschemamodel.ApplicationDefinition{SourceID: "task-1:create-order", Payload: []byte(`{"key":"order","name":"Order"}`)}
	replay := appschemamodel.ApplicationDefinitionUpsertRequest{SourceKind: "builder_v4", SourceID: before.SourceID, Payload: []byte(`{ "key": "order", "name": "Order" }`)}
	if err := metadataBuilderIdempotencyConflict(before, true, replay); err != nil {
		t.Fatalf("semantic replay rejected: %v", err)
	}
	reused := replay
	reused.Payload = []byte(`{"key":"order","name":"Changed"}`)
	if err := metadataBuilderIdempotencyConflict(before, true, reused); apperror.CodeOf(err) != idempotency.ErrorCodeKeyReused {
		t.Fatalf("key reuse error=%v", err)
	}
}
