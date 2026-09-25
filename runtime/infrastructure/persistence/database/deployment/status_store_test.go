package deployment

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"

	"github.com/domainry/domainry-foundation/idempotency"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	reportpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/report"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/timevalue"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

type contextRuntimeStatusContract interface {
	Ping(context.Context) error
	MigrationStatus(context.Context) (deploymentmodel.MigrationStatus, error)
}

type recordOperationFixture struct {
	id, workspace, status, key, fingerprint string
	leaseOwner, leaseExpires, expiresAt     string
	errorCode, createdAt, updatedAt         string
	fencingToken                            int64
}

func insertRecordOperationFixture(t *testing.T, store *database.RuntimeStore, value recordOperationFixture) {
	t.Helper()
	if value.key == "" {
		value.key = "key-" + value.id
	}
	if value.fingerprint == "" {
		value.fingerprint = "fingerprint-" + value.id
	}
	if value.fencingToken == 0 {
		value.fencingToken = 1
	}
	statement := store.InsertStatement("_operations", []string{
		"id", "workspace_id", "owner", "kind", "action_key", "resource_type", "resource_id", "idempotency_key", "request_fingerprint", "requested_by", "reason", "reference",
		"status", "status_url", "result_json", "metadata_json", "error_code", "failure_class", "next_action", "related_ids_json", "correlation", "evidence_json",
		"lease_owner", "lease_expires_at", "fencing_token", "expires_at", "created_at", "started_at", "finished_at", "updated_at",
	})
	if _, err := store.DB().ExecContext(t.Context(), statement,
		value.id, value.workspace, "record", "record.mutation", "create", "customer", "", value.id, value.fingerprint, "admin", "", value.key,
		value.status, "/operations/"+value.id, "{}", `{"response_status":409}`, value.errorCode, "", "", "[]", value.id, "[]",
		value.leaseOwner, timevalue.Millis(value.leaseExpires), value.fencingToken, timevalue.Millis(value.expiresAt), timevalue.Millis(value.createdAt), timevalue.Millis(value.createdAt), int64(0), timevalue.Millis(value.updatedAt),
	); err != nil {
		t.Fatal(err)
	}
}

func TestDeploymentStoreWorkspaceIsolationContract(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 19, 18, 0, 0, 0, time.UTC)
	insertReceipt := func(id, workspaceID string) {
		t.Helper()
		insertRecordOperationFixture(t, store, recordOperationFixture{id: id, workspace: workspaceID, status: string(idempotency.StatusFailedTerminal), errorCode: "failed", expiresAt: now.Add(time.Hour).Format(time.RFC3339Nano), createdAt: now.Format(time.RFC3339Nano), updatedAt: now.Format(time.RFC3339Nano)})
	}
	insertReceipt("receipt-a", "workspace-a")
	insertReceipt("receipt-b", "workspace-b")
	repository := NewRuntimeStatusStore(store)

	statusA, err := repository.IdempotencyOperationalStatus(t.Context(), "workspace-a", now)
	if err != nil || statusA.BacklogTotal != 1 {
		t.Fatalf("workspace A status=%#v err=%v", statusA, err)
	}
	receiptsA, err := repository.ListIdempotencyReceipts(t.Context(), "workspace-a", "", 10)
	if err != nil || len(receiptsA) != 1 || receiptsA[0].ID != "receipt-a" || receiptsA[0].WorkspaceID != "workspace-a" {
		t.Fatalf("workspace A receipts=%#v err=%v", receiptsA, err)
	}
	if changed, err := repository.ResetIdempotencyReceipt(t.Context(), "workspace-b", "record", "receipt-a"); err != nil || changed {
		t.Fatalf("cross-workspace reset changed=%v err=%v", changed, err)
	}
	if changed, err := repository.ResetIdempotencyReceipt(t.Context(), "workspace-a", "record", "receipt-a"); err != nil || !changed {
		t.Fatalf("workspace A reset changed=%v err=%v", changed, err)
	}

	if _, err := repository.IdempotencyOperationalStatus(t.Context(), "", now); !errors.Is(err, principalmodel.ErrWorkspaceIDRequired) {
		t.Fatalf("missing workspace status error=%v", err)
	}
	if _, err := repository.ListIdempotencyReceipts(t.Context(), "", "", 10); !errors.Is(err, principalmodel.ErrWorkspaceIDRequired) {
		t.Fatalf("missing workspace list error=%v", err)
	}
	if _, err := repository.RetryIdempotencyReceipt(t.Context(), "", "record", "receipt-a"); !errors.Is(err, principalmodel.ErrWorkspaceIDRequired) {
		t.Fatalf("missing workspace retry error=%v", err)
	}
	if _, err := repository.ResetIdempotencyReceipt(t.Context(), "", "record", "receipt-a"); !errors.Is(err, principalmodel.ErrWorkspaceIDRequired) {
		t.Fatalf("missing workspace reset error=%v", err)
	}
	if _, err := repository.IdempotencyOperationalStatusForSystem(t.Context(), principalmodel.SystemScope{}, now); !errors.Is(err, principalmodel.ErrSystemScopeRequired) {
		t.Fatalf("missing system scope status error=%v", err)
	}
	global, err := repository.IdempotencyOperationalStatusForSystem(t.Context(), principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "test deployment global status"), now)
	if err != nil || global.BacklogTotal != 2 {
		t.Fatalf("global status=%#v err=%v", global, err)
	}
}

func TestIdempotencyOperationalStatusAggregatesWorkspaceBacklogConflictsAndCleanup(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 19, 16, 0, 0, 0, time.UTC)
	insertReceipt := func(id, workspace, receiptStatus, leaseExpiresAt string) {
		t.Helper()
		insertRecordOperationFixture(t, store, recordOperationFixture{id: id, workspace: workspace, status: receiptStatus, leaseOwner: "worker", leaseExpires: leaseExpiresAt, expiresAt: now.Add(time.Hour).Format(time.RFC3339Nano), createdAt: now.Format(time.RFC3339Nano), updatedAt: now.Format(time.RFC3339Nano)})
	}
	insertReceipt("processing-expired", "workspace-a", string(idempotency.StatusProcessing), now.Add(-time.Minute).Format(time.RFC3339Nano))
	insertReceipt("retryable", "workspace-a", string(idempotency.StatusFailedRetryable), "")
	insertReceipt("other-workspace", "workspace-b", string(idempotency.StatusSucceeded), "")
	store.ObserveIdempotency(t.Context(), "workspace-a", "record.create", idempotency.OutcomeConflict)
	store.ObserveIdempotency(t.Context(), "workspace-b", "record.create", idempotency.OutcomeConflict)
	for index := 0; index < 5; index++ {
		store.ObserveIdempotency(t.Context(), "workspace-a", "record.create", idempotency.OutcomeLeaseLost)
	}
	store.ObserveIdempotency(t.Context(), "workspace-a", "record.create", idempotency.OutcomeDuplicateSideEffect)
	for index := 0; index < 20; index++ {
		store.ObserveIdempotency(t.Context(), "workspace-a", "record.create", idempotency.OutcomeAcquired)
	}
	for index := 0; index < 100; index++ {
		store.ObserveIdempotency(t.Context(), "workspace-a", "record.create", idempotency.OutcomeReplayed)
	}
	leaseInsert := store.InsertStatement("_worker_scopes", []string{"id", "owner", "scope_key", "lease_owner", "lease_expires_at", "fencing_token", "last_started_at", "last_completed_at", "checkpoint", "last_error", "updated_at"})
	if _, err := store.DB().ExecContext(t.Context(), leaseInsert, idempotencyCleanupLeaseID, "idempotency_cleanup", "receipts", "runtime-a", now.Add(time.Minute).UnixMilli(), 9, now.UnixMilli(), int64(0), 3, "", now.UnixMilli()); err != nil {
		t.Fatal(err)
	}

	status, err := NewRuntimeStatusStore(store).IdempotencyOperationalStatus(t.Context(), "workspace-a", now)
	if err != nil {
		t.Fatal(err)
	}
	if status.BacklogTotal != 2 || status.Backlog[string(idempotency.StatusProcessing)] != 1 || status.Backlog[string(idempotency.StatusFailedRetryable)] != 1 {
		t.Fatalf("workspace backlog=%#v total=%d", status.Backlog, status.BacklogTotal)
	}
	if status.Conflicts != 1 || status.ExpiredLeases != 1 {
		t.Fatalf("conflicts=%d expired_leases=%d", status.Conflicts, status.ExpiredLeases)
	}
	firing := map[string]bool{}
	for _, alert := range status.Alerts {
		firing[alert.Key] = alert.Status == "firing"
	}
	for _, key := range []string{"duplicate_side_effect", "lease_lost_spike", "processing_timeout", "replay_rate_high"} {
		if !firing[key] {
			t.Fatalf("expected firing alert %q in %#v", key, status.Alerts)
		}
	}
	if status.Cleanup.State != "running" || status.Cleanup.LeaseOwner != "runtime-a" || status.Cleanup.FencingToken != 9 {
		t.Fatalf("cleanup=%#v", status.Cleanup)
	}
}

func TestIdempotencyCleanupLeasePreventsConcurrentDeletionAndFencesReclaim(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 19, 14, 0, 0, 0, time.UTC)
	insertReceipt := func(id, status string, expiresAt time.Time) {
		t.Helper()
		insertRecordOperationFixture(t, store, recordOperationFixture{id: id, workspace: "workspace-a", status: status, expiresAt: expiresAt.Format(time.RFC3339Nano), createdAt: now.Add(-time.Hour).Format(time.RFC3339Nano), updatedAt: now.Add(-time.Hour).Format(time.RFC3339Nano)})
	}
	insertReceipt("expired-success", "succeeded", now.Add(-time.Minute))
	insertReceipt("expired-processing", "processing", now.Add(-time.Minute))
	insertReceipt("future-success", "succeeded", now.Add(time.Hour))
	leaseInsert := store.InsertStatement("_worker_scopes", []string{"id", "owner", "scope_key", "lease_owner", "lease_expires_at", "fencing_token", "last_started_at", "last_completed_at", "checkpoint", "last_error", "updated_at"})
	if _, err := store.DB().ExecContext(t.Context(), leaseInsert, idempotencyCleanupLeaseID, "idempotency_cleanup", "receipts", "runtime-a", now.Add(time.Minute).UnixMilli(), 7, now.UnixMilli(), int64(0), 0, "", now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	repository := NewRuntimeStatusStore(store)
	blocked, err := repository.RunIdempotencyCleanup(t.Context(), deploymentmodel.IdempotencyCleanupRequest{LeaseOwner: "runtime-b", LeaseTTL: time.Minute, BatchSize: 10, Now: now})
	if err != nil || blocked.Acquired || blocked.Deleted != 0 {
		t.Fatalf("live lease cleanup=%#v err=%v", blocked, err)
	}
	if _, err := store.DB().ExecContext(t.Context(), `UPDATE _worker_scopes SET lease_expires_at = ? WHERE id = ?`, now.Add(-time.Second).UnixMilli(), idempotencyCleanupLeaseID); err != nil {
		t.Fatal(err)
	}
	cleaned, err := repository.RunIdempotencyCleanup(t.Context(), deploymentmodel.IdempotencyCleanupRequest{LeaseOwner: "runtime-b", LeaseTTL: time.Minute, BatchSize: 10, Now: now})
	if err != nil || !cleaned.Acquired || cleaned.Deleted != 1 || cleaned.FencingToken != 8 {
		t.Fatalf("reclaimed cleanup=%#v err=%v", cleaned, err)
	}
	var remaining int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _operations WHERE owner = 'record'`).Scan(&remaining); err != nil || remaining != 2 {
		t.Fatalf("remaining=%d err=%v", remaining, err)
	}
}

func TestIdempotencyCleanupRevalidatesEligibilityAfterSelection(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 7, 15, 0, 0, 0, time.UTC)
	insertReceipt := func(id, status string) {
		t.Helper()
		insertRecordOperationFixture(t, store, recordOperationFixture{id: id, workspace: "workspace-a", status: status, expiresAt: now.Add(-time.Minute).Format(time.RFC3339Nano), createdAt: now.Add(-time.Hour).Format(time.RFC3339Nano), updatedAt: now.Add(-time.Hour).Format(time.RFC3339Nano)})
	}
	insertReceipt("selected-then-reclaimed", string(idempotency.StatusFailedRetryable))
	insertReceipt("expired-terminal", string(idempotency.StatusFailedTerminal))

	repository := NewRuntimeStatusStore(store)
	repository.beforeDeleteExpiredReceipts = func() {
		statement := `UPDATE _operations SET status = ?, expires_at = ?, lease_owner = ?, lease_expires_at = ?, fencing_token = fencing_token + 1 WHERE workspace_id = ? AND owner = 'record' AND id = ?`
		if _, err := store.DB().ExecContext(t.Context(), statement, string(idempotency.StatusProcessing), int64(0), "runtime-submit", now.Add(time.Minute).UnixMilli(), "workspace-a", "selected-then-reclaimed"); err != nil {
			t.Fatal(err)
		}
	}
	cleaned, err := repository.RunIdempotencyCleanup(t.Context(), deploymentmodel.IdempotencyCleanupRequest{LeaseOwner: "cleanup-a", LeaseTTL: time.Minute, BatchSize: 10, Now: now})
	if err != nil || cleaned.Deleted != 1 {
		t.Fatalf("cleanup=%+v err=%v", cleaned, err)
	}
	var status string
	var expiresAt int64
	if err = store.DB().QueryRowContext(t.Context(), `SELECT status, expires_at FROM _operations WHERE workspace_id = ? AND owner = 'record' AND id = ?`, "workspace-a", "selected-then-reclaimed").Scan(&status, &expiresAt); err != nil || status != string(idempotency.StatusProcessing) || expiresAt != 0 {
		t.Fatalf("reclaimed receipt status=%q expires_at=%d err=%v", status, expiresAt, err)
	}
	var terminalCount int
	if err = store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _operations WHERE workspace_id = ? AND owner = 'record' AND id = ?`, "workspace-a", "expired-terminal").Scan(&terminalCount); err != nil || terminalCount != 0 {
		t.Fatalf("expired terminal count=%d err=%v", terminalCount, err)
	}
}

func TestReportExportPrepareReceiptsAreVisibleAndUseUnifiedCleanup(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 7, 13, 0, 0, 0, time.UTC)
	receipts := reportpersistence.NewReportExportPrepareReceiptStore(store)
	claim, err := receipts.TryBeginReportExportPrepare(t.Context(), reportmodel.ReportExportPrepareClaimRequest{
		Receipt: reportmodel.ReportExportPrepareReceipt{
			WorkspaceID: "workspace-a", RequesterUserID: "requester-a", UseCase: reportmodel.ReportExportPrepareUseCase,
			ReportKey: "revenue", ObjectKey: "customer", AuditID: "audit-a", CallerKey: "private-caller-key",
		},
		RequestFingerprint: "private-request-fingerprint", LeaseOwner: "runtime-a", LeaseTTL: time.Minute, Now: now.Add(-time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := receipts.SaveReportExportPreparePayload(t.Context(), reportmodel.ReportExportPreparePayload{
		WorkspaceID: "workspace-a", ReceiptID: claim.Receipt.ID, PayloadJSON: `{"receipt":"report"}`, BusinessJobKey: "business-a",
		LeaseOwner: claim.Receipt.LeaseOwner, FencingToken: claim.Receipt.FencingToken, Now: now.Add(-time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = receipts.CompleteReportExportPrepare(t.Context(), reportmodel.ReportExportPrepareCompletion{
		WorkspaceID: "workspace-a", ReceiptID: receipt.ID, JobID: "job-a", LeaseOwner: receipt.LeaseOwner,
		FencingToken: receipt.FencingToken, Now: now.Add(-time.Hour), ExpiresAt: now.Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	for index, terminal := range []bool{false, true} {
		claim, claimErr := receipts.TryBeginReportExportPrepare(t.Context(), reportmodel.ReportExportPrepareClaimRequest{
			Receipt: reportmodel.ReportExportPrepareReceipt{
				WorkspaceID: "workspace-a", RequesterUserID: "requester-a", UseCase: reportmodel.ReportExportPrepareUseCase,
				ReportKey: "revenue", ObjectKey: "customer", AuditID: fmt.Sprintf("audit-failure-%d", index), CallerKey: fmt.Sprintf("caller-failure-%d", index),
			},
			RequestFingerprint: fmt.Sprintf("fingerprint-failure-%d", index), LeaseOwner: "runtime-a", LeaseTTL: time.Minute, Now: now.Add(-time.Hour),
		})
		if claimErr != nil {
			t.Fatal(claimErr)
		}
		failureReceipt, saveErr := receipts.SaveReportExportPreparePayload(t.Context(), reportmodel.ReportExportPreparePayload{
			WorkspaceID: "workspace-a", ReceiptID: claim.Receipt.ID, PayloadJSON: fmt.Sprintf(`{"receipt":%q}`, claim.Receipt.ID), BusinessJobKey: fmt.Sprintf("business-failure-%d", index),
			LeaseOwner: claim.Receipt.LeaseOwner, FencingToken: claim.Receipt.FencingToken, Now: now.Add(-time.Hour),
		})
		if saveErr != nil {
			t.Fatal(saveErr)
		}
		failure := reportmodel.ReportExportPrepareFailure{
			WorkspaceID: "workspace-a", ReceiptID: failureReceipt.ID, LeaseOwner: failureReceipt.LeaseOwner,
			FencingToken: failureReceipt.FencingToken, ErrorCode: "backend.report.export_failure", Now: now.Add(-time.Hour), ExpiresAt: now.Add(-time.Minute),
		}
		if terminal {
			claimErr = receipts.FailReportExportPrepareTerminal(t.Context(), failure)
		} else {
			claimErr = receipts.FailReportExportPrepareRetryable(t.Context(), failure)
		}
		if claimErr != nil {
			t.Fatal(claimErr)
		}
	}

	statusStore := NewRuntimeStatusStore(store)
	listed, err := statusStore.ListIdempotencyReceipts(t.Context(), "workspace-a", "succeeded", 50)
	if err != nil {
		t.Fatal(err)
	}
	var reportReceipt *idempotency.ReceiptSummary
	for index := range listed {
		if listed[index].Owner == "report" {
			reportReceipt = &listed[index]
			break
		}
	}
	if reportReceipt == nil || reportReceipt.ID != receipt.ID || reportReceipt.Scope != reportmodel.ReportExportPrepareUseCase ||
		strings.Contains(reportReceipt.IdempotencyKeyHash+reportReceipt.RequestFingerprintHash, "private-") {
		t.Fatalf("listed report receipt=%#v all=%#v", reportReceipt, listed)
	}
	cleaned, err := statusStore.RunIdempotencyCleanup(t.Context(), deploymentmodel.IdempotencyCleanupRequest{LeaseOwner: "cleanup-a", LeaseTTL: time.Minute, BatchSize: 50, Now: now})
	if err != nil || cleaned.Deleted != 3 {
		t.Fatalf("cleanup=%#v err=%v", cleaned, err)
	}
	if _, found, err := receipts.GetReportExportPrepareReceipt(t.Context(), "workspace-a", receipt.ID); err != nil || found {
		t.Fatalf("expired report receipt found=%v err=%v", found, err)
	}
	var remaining int
	if err = store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _operations WHERE workspace_id = ? AND owner = 'report'`, "workspace-a").Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("remaining report receipts=%d err=%v", remaining, err)
	}
}

func TestRuntimeStatusStoreListsSanitizedReceiptsAndGuardsTransitions(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	now := "2026-07-19T12:00:00Z"
	insertRecordOperationFixture(t, store, recordOperationFixture{id: "receipt-1", workspace: "workspace-a", status: "failed_terminal", key: "raw-private-key", fingerprint: "raw-fingerprint", leaseOwner: "owner-a", leaseExpires: now, fencingToken: 4, expiresAt: now, errorCode: "failed", createdAt: now, updatedAt: now})
	repository := NewRuntimeStatusStore(store)
	receipts, err := repository.ListIdempotencyReceipts(t.Context(), "workspace-a", "", 10)
	if err != nil || len(receipts) != 1 {
		t.Fatalf("receipts=%#v err=%v", receipts, err)
	}
	encoded := receipts[0].IdempotencyKeyHash + receipts[0].RequestFingerprintHash
	if receipts[0].Scope != "record.create" || receipts[0].Status != "failed_terminal" || strings.Contains(encoded, "raw-private-key") || strings.Contains(encoded, "raw-fingerprint") {
		t.Fatalf("unsafe receipt=%#v", receipts[0])
	}
	if changed, err := repository.RetryIdempotencyReceipt(t.Context(), "workspace-a", "record", "receipt-1"); err != nil || changed {
		t.Fatalf("terminal receipt retry changed=%v err=%v", changed, err)
	}
	if changed, err := repository.ResetIdempotencyReceipt(t.Context(), "workspace-b", "record", "receipt-1"); err != nil || changed {
		t.Fatalf("cross-workspace reset changed=%v err=%v", changed, err)
	}
	if changed, err := repository.ResetIdempotencyReceipt(t.Context(), "workspace-a", "record", "receipt-1"); err != nil || !changed {
		t.Fatalf("reset changed=%v err=%v", changed, err)
	}
	if changed, err := repository.RetryIdempotencyReceipt(t.Context(), "workspace-a", "record", "receipt-1"); err != nil || !changed {
		t.Fatalf("retry changed=%v err=%v", changed, err)
	}
	receipts, err = repository.ListIdempotencyReceipts(t.Context(), "workspace-a", "failed_retryable", 10)
	if err != nil || len(receipts) != 1 || receipts[0].FencingToken != 6 {
		t.Fatalf("transitioned receipts=%#v err=%v", receipts, err)
	}
}

func openStoreForGeneratedListTest(t *testing.T) *database.RuntimeStore {
	t.Helper()
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "deployment.db")})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

var _ contextRuntimeStatusContract = RuntimeStatusStore{}

func TestRuntimeStatusStoreContractAndCancellation(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewRuntimeStatusStore(store)
	if err := repository.Ping(t.Context()); err != nil {
		t.Fatalf("ping: %v", err)
	}
	status, err := repository.MigrationStatus(t.Context())
	if err != nil {
		t.Fatalf("migration status: %v", err)
	}
	if status.Rollback.Mode != "restore_sqlite_backup" || !status.Rollback.RequiresVerifiedBackup {
		t.Fatalf("migration rollback policy=%+v", status.Rollback)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := repository.Ping(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled ping error=%v", err)
	}
	if _, err := repository.MigrationStatus(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled migration status error=%v", err)
	}
}
