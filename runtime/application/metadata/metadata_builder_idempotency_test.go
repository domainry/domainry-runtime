package metadata

import (
	"testing"

	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	"github.com/domainry/domainry-runtime/runtime/platform/idempotency"
)

func TestMetadataBuilderIdempotencyRejectsSameKeyWithDifferentPayload(t *testing.T) {
	before := metadatamodel.MetadataDefinition{SourceID: "task-1:create-order", Payload: []byte(`{"key":"order","name":"Order"}`)}
	replay := metadatamodel.MetadataDefinitionUpsertRequest{SourceKind: "builder_v4", SourceID: before.SourceID, Payload: []byte(`{ "key": "order", "name": "Order" }`)}
	if err := metadataBuilderIdempotencyConflict(before, true, replay); err != nil {
		t.Fatalf("semantic replay rejected: %v", err)
	}
	reused := replay
	reused.Payload = []byte(`{"key":"order","name":"Changed"}`)
	if err := metadataBuilderIdempotencyConflict(before, true, reused); apperror.CodeOf(err) != idempotency.ErrorCodeKeyReused {
		t.Fatalf("key reuse error=%v", err)
	}
}
