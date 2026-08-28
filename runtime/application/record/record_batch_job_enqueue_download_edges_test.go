package record

import (
	"context"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func TestRecordBatchExportAuthorizationAndCorruptReplayFailures(t *testing.T) {
	principal := recordBatchPrincipal()
	store := &recordBatchStoreProbe{jobs: map[string]recordmodel.RecordBatchJob{}}
	exporter := NewRecordExportApplicationService(RecordExportDependencies{Objects: func() map[string]definitionmodel.ObjectSchema { return map[string]definitionmodel.ObjectSchema{} }})
	service := NewRecordBatchJobApplicationService(RecordBatchJobDependencies{Store: store, Exporter: exporter})
	if _, _, err := service.EnqueueExport(t.Context(), "missing", "key", RecordExportOptions{}, principal); apperror.CodeOf(err) != "backend.object.not_found" {
		t.Fatalf("authorization err=%v", err)
	}
	store.jobs["corrupt"] = recordmodel.RecordBatchJob{
		ID: "corrupt", WorkspaceID: principal.WorkspaceID, Kind: "export", ObjectKey: "customer", IdempotencyKey: "corrupt-key", PayloadJSON: "{",
	}
	if _, _, err := service.EnqueueExport(t.Context(), "customer", "corrupt-key", RecordExportOptions{}, principal); apperror.CodeOf(err) != "backend.idempotency.key_reused" {
		t.Fatalf("corrupt replay err=%v", err)
	}
	store.jobs["mismatch"] = recordmodel.RecordBatchJob{
		ID: "mismatch", WorkspaceID: principal.WorkspaceID, Kind: "export", ObjectKey: "customer", IdempotencyKey: "mismatch-key", PayloadJSON: `{"options":{"reason":"first"}}`,
	}
	if _, _, err := service.EnqueueExport(t.Context(), "customer", "mismatch-key", RecordExportOptions{Reason: "second"}, principal); apperror.CodeOf(err) != "backend.idempotency.key_reused" {
		t.Fatalf("mismatch replay err=%v", err)
	}
}

func TestRecordBatchDownloadAuditsSuccessfulPayload(t *testing.T) {
	principal := recordBatchPrincipal()
	store := &recordBatchStoreProbe{
		jobs: map[string]recordmodel.RecordBatchJob{"export": {
			ID: "export", WorkspaceID: principal.WorkspaceID, Kind: "export", Status: "completed", ObjectKey: "customer", Total: 2,
			PayloadJSON: `{"options":{"fields":["name"],"reason":"audit"},"assurance_evidence":{"grant_id":"grant"}}`,
		}},
		chunks: map[string][]recordmodel.RecordBatchJobChunk{"export": {{JobID: "export", Content: "name\nAcme\n"}}},
	}
	audited := false
	service := NewRecordBatchJobApplicationService(RecordBatchJobDependencies{
		Store: store,
		Audit: func(_ context.Context, event, _, _ string, _ principalmodel.Principal, _ string, _, _, metadata map[string]any) {
			audited = event == "record_batch_export_downloaded" && metadata["download_sha256"] != "" && metadata["download_bytes"] == len("name\nAcme\n")
		},
	})
	if _, _, err := service.Download(t.Context(), "export", principal); err != nil || !audited {
		t.Fatalf("audited=%v err=%v", audited, err)
	}
}
