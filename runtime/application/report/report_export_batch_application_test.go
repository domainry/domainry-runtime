package report

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
	reportservice "github.com/domainry/domainry-runtime/runtime/domain/report/service"
)

func TestReportExportRecordStringNeverSerializesNilSentinel(t *testing.T) {
	for name, data := range map[string]map[string]any{
		"missing": {},
		"nil":     {"token": nil},
		"empty":   {"token": "  "},
	} {
		t.Run(name, func(t *testing.T) {
			if got := reportExportRecordString(data, "token"); got != "" {
				t.Fatalf("record string=%q", got)
			}
		})
	}
	projection := ReportExportJob{ID: "job", Status: "accepted"}
	raw, err := json.Marshal(projection)
	if err != nil || strings.Contains(string(raw), "<nil>") || strings.Contains(string(raw), "download_token") {
		t.Fatalf("projection JSON=%s err=%v", raw, err)
	}
}

func TestCompletedReportExportJobRequiresAndReturnsFormalDownloadProjection(t *testing.T) {
	principal := reportPrincipal()
	payload := reportExportBatchPayload{ReportKey: "ledger", ObjectKey: "ledger", ArtifactIdempotencyKey: "export-key", ExactTotal: 1001}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	job := recordmodel.RecordBatchJob{ID: "job-1", Status: "completed", PayloadJSON: string(payloadJSON), ResultArtifactID: "artifact-1", Total: 1001, Checkpoint: 1001}
	mapping := reportExportTestRecordMapping()
	control := reportmodel.ReportExportControlSchema{ReportKey: "ledger", SourceObjects: []string{"ledger"}, DownloadObject: "report_export_download", RecordMapping: mapping}
	store := &reportExportStoreStub{download: recordmodel.Record{ID: "download-1", Data: map[string]any{
		mapping.DownloadTokenField: "token-1", mapping.DownloadExpiresAtField: "2026-08-25T09:00:00Z",
		mapping.DownloadContentHashField: "sha256:" + strings.Repeat("a", 64),
	}}}
	artifacts := &reportExportArtifactStoreStub{artifact: reportmodel.ReportExportArtifact{
		ID: "artifact-1", WorkspaceID: principal.WorkspaceID, RequesterUserID: principal.UserID,
		ReportKey: "ledger", IdempotencyKey: "export-key", BusinessDownloadID: "download-1",
	}}
	service := &ReportApplicationService{
		exportRecords: store, exportArtifacts: artifacts,
		exportControls: func(context.Context, principalmodel.Principal) []reportmodel.ReportExportControlSchema {
			return []reportmodel.ReportExportControlSchema{control}
		},
	}
	projection, err := service.reportExportJob(t.Context(), job, principal)
	if err != nil || projection.Status != "completed" || projection.DownloadToken != "token-1" ||
		projection.ContentSHA256 != strings.Repeat("a", 64) || projection.ArtifactID != "artifact-1" {
		t.Fatalf("completed projection=%+v err=%v", projection, err)
	}
	store.download.Data[mapping.DownloadTokenField] = nil
	if _, err := service.reportExportJob(t.Context(), job, principal); apperror.CodeOf(err) != "backend.report.export_projection_incomplete" {
		t.Fatalf("incomplete completed projection err=%v", err)
	}
}

type concurrentReplayAuditStore struct {
	*reportExportStoreStub
	mu    sync.Mutex
	calls int
}

func (s *concurrentReplayAuditStore) UpdateReportRecord(_ context.Context, objectKey, recordID string, patch map[string]any, _ string, _ principalmodel.Principal) (recordmodel.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.calls < 8 {
		return recordmodel.Record{}, &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.idempotency.in_progress"}
	}
	for key, value := range patch {
		s.audit.Data[key] = value
	}
	return s.audit, nil
}

func TestCompletedReportExportReplayWaitsForConcurrentAuditMutation(t *testing.T) {
	principal := reportPrincipal()
	store := &concurrentReplayAuditStore{reportExportStoreStub: &reportExportStoreStub{audit: recordmodel.Record{ID: "audit-new", Data: map[string]any{"status": "approved"}}}}
	service := &ReportApplicationService{exportRecords: store}
	errors := make(chan error, 8)
	var callers sync.WaitGroup
	for index := 0; index < 8; index++ {
		callers.Add(1)
		go func() {
			defer callers.Done()
			errors <- service.closeReportExportReplayAuditRecord(t.Context(), "report_export_audit", "audit-new", map[string]any{"status": "completed", "row_count": 1001}, "report-export-replay:job-old:audit-new", principal)
		}()
	}
	callers.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatalf("equivalent concurrent replay leaked mutation conflict: %v", err)
		}
	}
	if store.calls < 15 || store.audit.Data["status"] != "completed" || store.audit.Data["row_count"] != 1001 {
		t.Fatalf("audit replay did not converge: calls=%d audit=%#v", store.calls, store.audit.Data)
	}
}

type reportExportBatchWriterProbe struct {
	chunks    []recordmodel.RecordBatchJobChunk
	failAfter int
}

func (w *reportExportBatchWriterProbe) CommitPage(_ context.Context, job *recordmodel.RecordBatchJob, content, nextCursor string, processed, total int) error {
	if w.failAfter >= 0 && len(w.chunks) == w.failAfter {
		return fmt.Errorf("simulated crash before page commit")
	}
	w.chunks = append(w.chunks, recordmodel.RecordBatchJobChunk{WorkspaceID: job.WorkspaceID, JobID: job.ID, Sequence: len(w.chunks), Content: content})
	job.Checkpoint = processed
	job.Total = total
	job.CheckpointCursor = nextCursor
	job.ResultChunks = len(w.chunks)
	return nil
}

func (w *reportExportBatchWriterProbe) Chunks(_ context.Context, _ recordmodel.RecordBatchJob) ([]recordmodel.RecordBatchJobChunk, error) {
	return append([]recordmodel.RecordBatchJobChunk(nil), w.chunks...), nil
}

func TestReportExportAuditBindingUsesDurableJobIdentity(t *testing.T) {
	job := recordmodel.RecordBatchJob{ID: "job-1", AuditID: " report_export_audit_1 "}
	if got, err := reportExportAuditBinding(job, ""); err != nil || got != "report_export_audit_1" {
		t.Fatalf("empty canonical payload binding=%q err=%v", got, err)
	}
	if got, err := reportExportAuditBinding(job, "report_export_audit_1"); err != nil || got != "report_export_audit_1" {
		t.Fatalf("matching legacy payload binding=%q err=%v", got, err)
	}
	if _, err := reportExportAuditBinding(job, "report_export_audit_other"); apperror.CodeOf(err) != "backend.report.export_audit_binding_changed" {
		t.Fatalf("mismatched payload err=%v", err)
	}
	job.AuditID = ""
	if _, err := reportExportAuditBinding(job, "report_export_audit_1"); apperror.CodeOf(err) != "backend.report.export_audit_binding_invalid" {
		t.Fatalf("missing durable binding err=%v", err)
	}
}

func TestSmallExportWorkerProducesRealDurableArtifact(t *testing.T) {
	principal := reportPrincipal()
	now := time.Date(2026, 8, 22, 10, 0, 0, 0, time.UTC)
	report := reportmodel.ReportSchema{Key: "ledger", ObjectSQLV1: &reportmodel.ReportObjectSQLSchema{
		SQL: "SELECT l.id AS id FROM ledger l ORDER BY l.id LIMIT 2000", SourceObjects: []string{"ledger"},
		ResultSchema: []reportmodel.ReportResultColumnSchema{{Key: "id", Type: "text", Kind: "dimension"}},
	}}
	executor := &reportExportObjectSQLExecutorStub{rows: []map[string]string{{"id": "ledger-0001"}, {"id": "ledger-0002"}}}
	domain := reportservice.NewReportDomainService(reportservice.ReportDependencies{
		Reports: func(context.Context, principalmodel.Principal) []reportmodel.ReportSchema {
			return []reportmodel.ReportSchema{report}
		},
		Access: reportExportDatasetAccessStub{objects: map[string]definitionmodel.ObjectSchema{
			"ledger": {Key: "ledger", Fields: []definitionmodel.FieldSchema{{Key: "id", Type: "text"}}},
		}},
		ObjectSQL: executor, SnapshotSources: reportSnapshotSourceStub{},
	})
	records := &reportExportStoreStub{audit: recordmodel.Record{ID: "audit-small", Data: map[string]any{
		"report_key": report.Key, "requested_by_identity_user_id": principal.UserID, "status": "approved",
	}}}
	artifacts := &reportExportArtifactStoreStub{}
	control := reportmodel.ReportExportControlSchema{
		Key: "ledger-export", ReportKey: report.Key, SourceObjects: []string{"ledger"}, AuditObject: "report_export_audit", DownloadObject: "report_export_download",
		ExportAction: "report.export", MaxRows: 2000, RecordMapping: reportExportTestRecordMapping(),
	}
	service := NewReportApplicationService(ReportApplicationDependencies{
		Domain: domain, Records: &reportRecordExporterStub{}, ExportRecords: records, ExportArtifacts: artifacts, Audit: &reportAuditAppenderStub{},
		ExportControls: func(context.Context, principalmodel.Principal) []reportmodel.ReportExportControlSchema {
			return []reportmodel.ReportExportControlSchema{control}
		},
		Clock: func() time.Time { return now }, CursorKey: []byte("export-cursor-key"),
	})
	scope := reportmodel.ReportExportScopeRequest{FieldProjection: []string{"id"}, Purpose: "durable artifact verification", Freshness: reportmodel.ReportExportFreshness{Mode: "realtime"}}
	payload, err := service.prepareReportExportPayload(t.Context(), report.Key, "ledger", "audit-small", "caller-key", scope, principal)
	if err != nil || payload.ExactTotal != 2 {
		t.Fatalf("small payload total=%d err=%v", payload.ExactTotal, err)
	}
	if reportExportRoutesAsync(payload.ExactTotal) {
		t.Fatal("small dataset unexpectedly crossed the asynchronous export threshold")
	}
	payload.ArtifactIdempotencyKey = "report-export-artifact:" + reportExportRequestFingerprint(payload)
	payload.AuditID = ""
	raw, _ := json.Marshal(payload)
	job := recordmodel.RecordBatchJob{ID: "job-small", WorkspaceID: principal.WorkspaceID, Kind: reportExportBatchKind, ObjectKey: "ledger", Status: "running", PayloadJSON: string(raw), AuditID: "audit-small", ActorID: principal.UserID}
	writer := &reportExportBatchWriterProbe{failAfter: -1}
	if err := service.processReportExportBatch(t.Context(), &job, principal, writer); err != nil {
		t.Fatal(err)
	}
	if job.Checkpoint != 2 || job.Total != 2 || job.ResultArtifactID != "artifact-1" || artifacts.artifact.RowCount != 2 || len(artifacts.artifact.Content) == 0 {
		t.Fatalf("job=%+v artifact=%+v", job, artifacts.artifact)
	}
	parsed, err := csv.NewReader(strings.NewReader(string(artifacts.artifact.Content))).ReadAll()
	if err != nil || len(parsed) != 3 || parsed[1][0] != "ledger-0001" || parsed[2][0] != "ledger-0002" {
		t.Fatalf("durable artifact rows=%v err=%v", parsed, err)
	}
}

func TestReportExportBatchExecutesFrozenReportInOpaqueCursorPages(t *testing.T) {
	principal := reportPrincipal()
	now := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	report := reportmodel.ReportSchema{Key: "ledger", ObjectSQLV1: &reportmodel.ReportObjectSQLSchema{
		SQL:           "SELECT l.id AS id FROM ledger l ORDER BY l.id LIMIT 2000",
		SourceObjects: []string{"ledger"},
		ResultSchema:  []reportmodel.ReportResultColumnSchema{{Key: "id", Type: "text", Kind: "dimension"}},
	}}
	rows := make([]map[string]string, 1001)
	for index := range rows {
		rows[index] = map[string]string{"id": fmt.Sprintf("ledger-%04d", index)}
	}
	executor := &reportExportObjectSQLExecutorStub{rows: rows}
	domain := reportservice.NewReportDomainService(reportservice.ReportDependencies{
		Reports: func(context.Context, principalmodel.Principal) []reportmodel.ReportSchema {
			return []reportmodel.ReportSchema{report}
		},
		Access: reportExportDatasetAccessStub{objects: map[string]definitionmodel.ObjectSchema{
			"ledger": {Key: "ledger", Fields: []definitionmodel.FieldSchema{{Key: "id", Type: "text"}}},
		}},
		ObjectSQL: executor, SnapshotSources: reportSnapshotSourceStub{},
	})
	records := &reportExportStoreStub{audit: recordmodel.Record{ID: "audit-1", Data: map[string]any{
		"report_key": report.Key, "requested_by_identity_user_id": principal.UserID, "status": "approved",
	}}}
	artifacts := &reportExportArtifactStoreStub{}
	control := reportmodel.ReportExportControlSchema{
		Key: "ledger-export", ReportKey: report.Key, SourceObjects: []string{"ledger"}, AuditObject: "report_export_audit", DownloadObject: "report_export_download",
		ExportAction: "report.export", MaxRows: 2000, RecordMapping: reportExportTestRecordMapping(),
	}
	service := NewReportApplicationService(ReportApplicationDependencies{
		Domain: domain, Records: &reportRecordExporterStub{}, ExportRecords: records, ExportArtifacts: artifacts, Audit: &reportAuditAppenderStub{},
		ExportControls: func(context.Context, principalmodel.Principal) []reportmodel.ReportExportControlSchema {
			return []reportmodel.ReportExportControlSchema{control}
		},
		Clock: func() time.Time { return now }, CursorKey: []byte("export-cursor-key"),
	})
	scope := reportmodel.ReportExportScopeRequest{FieldProjection: []string{"id"}, Purpose: "payroll reconciliation", Freshness: reportmodel.ReportExportFreshness{Mode: "realtime"}}
	payload, err := service.prepareReportExportPayload(t.Context(), report.Key, "ledger", "audit-1", "caller-key", scope, principal)
	if err != nil || payload.ExactTotal != 1001 {
		t.Fatalf("payload total=%d err=%v", payload.ExactTotal, err)
	}
	payload.ArtifactIdempotencyKey = "report-export-artifact:" + reportExportRequestFingerprint(payload)
	// The persisted payload is canonical and intentionally excludes request
	// evidence; record_batch_jobs.audit_id is the frozen audit binding.
	payload.AuditID = ""
	raw, _ := json.Marshal(payload)
	job := recordmodel.RecordBatchJob{ID: "job-1", WorkspaceID: principal.WorkspaceID, Kind: reportExportBatchKind, ObjectKey: "ledger", Status: "running", PayloadJSON: string(raw), AuditID: "audit-1", ActorID: principal.UserID}
	writer := &reportExportBatchWriterProbe{failAfter: 2}
	if err := service.processReportExportBatch(t.Context(), &job, principal, writer); err == nil || job.Checkpoint != 400 || job.ResultChunks != 2 {
		t.Fatalf("crash checkpoint job=%+v err=%v", job, err)
	}
	writer.failAfter = -1
	// Reconstruct the claimed job as a restarted worker would: progress and the
	// official audit column survive while payload.audit_id remains empty.
	restartedJob := job
	if err := service.processReportExportBatch(t.Context(), &restartedJob, principal, writer); err != nil {
		t.Fatal(err)
	}
	job = restartedJob
	if job.Checkpoint != 1001 || job.Total != 1001 || job.ResultChunks != 6 || job.CheckpointCursor != "" || job.ResultArtifactID != "artifact-1" {
		t.Fatalf("job=%+v", job)
	}
	var output strings.Builder
	for _, chunk := range writer.chunks {
		output.WriteString(chunk.Content)
	}
	parsed, err := csv.NewReader(strings.NewReader(output.String())).ReadAll()
	if err != nil || len(parsed) != 1002 || parsed[1][0] != "ledger-0000" || parsed[1001][0] != "ledger-1000" {
		t.Fatalf("paged CSV rows=%d first/last=%v/%v err=%v", len(parsed), parsed[1], parsed[len(parsed)-1], err)
	}
	seen := map[string]bool{}
	for _, row := range parsed[1:] {
		if seen[row[0]] {
			t.Fatalf("duplicate ledger row %s", row[0])
		}
		seen[row[0]] = true
	}
	if len(executor.requests) != 13 { // six bounded routing probes plus bounded failed/resumed worker pages
		t.Fatalf("same Report executions=%d", len(executor.requests))
	}
	full, bounded := 0, 0
	for _, request := range executor.requests {
		if request.PageSize == 0 {
			full++
		} else if request.PageSize > 0 && request.PageSize <= reportmodel.ReportPageMaximumSize {
			bounded++
		}
	}
	if full != 0 || bounded != 13 {
		t.Fatalf("full/bounded Report executions=%d/%d requests=%+v", full, bounded, executor.requests)
	}
}

func TestReportExportReplayClosesNewAuditWithoutChangingExecutionIdentity(t *testing.T) {
	principal := reportPrincipal()
	records := &reportExportStoreStub{audit: recordmodel.Record{ID: "audit-b", Data: map[string]any{
		"report_key": "ledger", "requested_by_identity_user_id": principal.UserID, "status": "approved",
	}}}
	audits := &reportAuditAppenderStub{}
	control := reportmodel.ReportExportControlSchema{
		ReportKey: "ledger", SourceObjects: []string{"ledger"}, AuditObject: "report_export_audit",
		RecordMapping: reportExportTestRecordMapping(),
	}
	service := &ReportApplicationService{
		exportRecords: records, audit: audits,
		exportControls: func(context.Context, principalmodel.Principal) []reportmodel.ReportExportControlSchema {
			return []reportmodel.ReportExportControlSchema{control}
		},
	}
	payload := reportExportBatchPayload{
		ReportKey: "ledger", ObjectKey: "ledger", AuditID: "audit-b", ExactTotal: 1001,
		Scope: reportmodel.ReportExportScopeRequest{Purpose: "same export", Freshness: reportmodel.ReportExportFreshness{Mode: "realtime"}},
	}
	job := recordmodel.RecordBatchJob{ID: "job-a", AuditID: "audit-a", Status: "running"}
	projection := ReportExportJob{ID: job.ID, AuditID: job.AuditID, Status: "running"}
	if err := service.closeReportExportReplayAudit(t.Context(), payload, job, projection, principal); err != nil {
		t.Fatal(err)
	}
	if records.audit.Data["row_count"] != 1001 || !strings.HasPrefix(fmt.Sprint(records.audit.Data["filters_hash"]), "sha256:") || records.audit.Data["status"] != "approved" {
		t.Fatalf("active replay audit=%#v", records.audit.Data)
	}
	if len(audits.requests) != 1 || audits.requests[0].Event != "report_export_prepare_replayed" || audits.requests[0].RecordID != "audit-b" || audits.requests[0].Metadata["job_id"] != "job-a" || audits.requests[0].Metadata["original_audit_id"] != "audit-a" {
		t.Fatalf("replay evidence=%#v", audits.requests)
	}

	job.Status, projection.Status, projection.ArtifactID = "completed", "completed", "artifact-a"
	records.audit.Data["status"] = "approved"
	if err := service.closeReportExportReplayAudit(t.Context(), payload, job, projection, principal); err != nil {
		t.Fatal(err)
	}
	if records.audit.Data["status"] != control.RecordMapping.AuditPreparedStatus || audits.requests[1].Metadata["artifact_id"] != "artifact-a" {
		t.Fatalf("completed replay audit=%#v evidence=%#v", records.audit.Data, audits.requests[1])
	}
}
