package record

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	dataexchange "github.com/domainry/domainry-data-exchange-sdk"
	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func recordExportDispatchService(total int) (*RecordDataExchangeApplicationService, *recordDataExchangeBindingProbe) {
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	repository := &exportEdgeRepository{list: func(_ context.Context, _ string, _ definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
		if query.PageSize == 1 && !query.SkipTotal {
			return recordmodel.RecordPageResult{Total: total}, nil
		}
		return recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "customer-1", Data: map[string]any{"name": "Acme"}}}}, nil
	}}
	exporter := recordExportEdgeService(object, repository)
	binding := &recordDataExchangeBindingProbe{}
	return NewRecordDataExchangeApplicationService(RecordDataExchangeDependencies{Exporter: exporter, DataExchange: binding}), binding
}

func TestRecordExportDispatchSelectsDeliveryWithoutCallerThresholdLogic(t *testing.T) {
	principal := recordExportPrincipal("customer")
	direct, binding := recordExportDispatchService(recordExportDirectMaxRows)
	result, err := direct.DispatchExportIdempotent(t.Context(), "customer", "direct-key", RecordExportOptions{}, principal)
	if err != nil || result.Delivery != RecordExportDeliveryDirect || result.Filename != "customer.csv" || !strings.Contains(string(result.Content), "Acme") || binding.lastExport.Provider != "" {
		t.Fatalf("direct=%+v submitted=%+v err=%v", result, binding.lastExport, err)
	}

	background, binding := recordExportDispatchService(recordExportDirectMaxRows + 1)
	result, err = background.DispatchExportIdempotent(t.Context(), "customer", "background-key", RecordExportOptions{}, principal)
	if err != nil || result.Delivery != RecordExportDeliveryBackground || result.Job.ID == "" || binding.lastExport.Provider != "records" {
		t.Fatalf("background=%+v submitted=%+v err=%v", result, binding.lastExport, err)
	}
	var payload recordDataExchangeExportPayload
	if err = json.Unmarshal(binding.lastExport.Options, &payload); err != nil || payload.ExactTotal == nil || *payload.ExactTotal != recordExportDirectMaxRows+1 {
		t.Fatalf("payload=%+v err=%v", payload, err)
	}

	if _, err = background.DispatchExportIdempotent(t.Context(), "customer", "", RecordExportOptions{}, principal); apperror.CodeOf(err) != "backend.idempotency.key_required" {
		t.Fatalf("missing key error=%v", err)
	}
}

func TestRecordExportDownloadIsOwnerScopedAndRequiresCompletedArtifact(t *testing.T) {
	service, binding := recordExportDispatchService(recordExportDirectMaxRows + 1)
	principal := recordExportPrincipal("customer")
	principal.UserID = "actor"
	binding.jobs = map[string]dataexchange.Job{
		"job": {ID: "job", Provider: "records", Operation: "export", Status: "completed", WorkspaceID: principal.WorkspaceID, ActorID: principal.UserID, ObjectKey: "customer", ArtifactID: "artifact"},
	}
	binding.artifact = dataexchange.Artifact{ID: "artifact", Filename: "customer.csv", ContentType: "text/csv", Size: 4}
	binding.artifactContent = "data"
	artifact, err := service.DownloadExport(t.Context(), "job", principal)
	if err != nil {
		t.Fatal(err)
	}
	defer artifact.Content.Close()
	content, _ := io.ReadAll(artifact.Content)
	if string(content) != "data" || artifact.Filename != "customer.csv" {
		t.Fatalf("artifact=%+v content=%q", artifact, content)
	}

	other := principal
	other.UserID = "other"
	if _, err = service.DownloadExport(t.Context(), "job", other); apperror.CodeOf(err) != "backend.export.job_not_found" {
		t.Fatalf("cross-owner download error=%v", err)
	}
	binding.jobs["job"] = dataexchange.Job{ID: "job", Provider: "records", Operation: "export", Status: "running", WorkspaceID: principal.WorkspaceID, ActorID: principal.UserID}
	if _, err = service.DownloadExport(t.Context(), "job", principal); apperror.CodeOf(err) != "backend.export.download_not_ready" {
		t.Fatalf("running download error=%v", err)
	}
}
