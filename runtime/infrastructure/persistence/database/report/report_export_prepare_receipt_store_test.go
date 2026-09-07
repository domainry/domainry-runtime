package report

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/idempotency"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestConcurrentReportExportPrepareDifferentCallersHaveOneAuditWinner(t *testing.T) {
	runtimeStore := openReportExportReceiptRuntimeStore(t, filepath.Join(t.TempDir(), "report-export-audit-winner.db"))
	defer runtimeStore.Close()
	receipts := NewReportExportPrepareReceiptStore(runtimeStore)
	now := time.Date(2026, 9, 7, 9, 30, 0, 0, time.UTC)
	requests := []reportmodel.ReportExportPrepareClaimRequest{
		reportExportPrepareClaimFixture("workspace-a", "requester-a", "audit-a", "caller-a", "fingerprint-a", now),
		reportExportPrepareClaimFixture("workspace-a", "requester-a", "audit-a", "caller-b", "fingerprint-a", now),
	}

	type outcome struct {
		claim reportmodel.ReportExportPrepareClaimResult
		err   error
	}
	start := make(chan struct{})
	results := make(chan outcome, len(requests))
	var wait sync.WaitGroup
	for _, request := range requests {
		request := request
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			claim, err := receipts.TryBeginReportExportPrepare(t.Context(), request)
			results <- outcome{claim: claim, err: err}
		}()
	}
	close(start)
	wait.Wait()
	close(results)

	acquired, auditConflicts := 0, 0
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		switch {
		case result.claim.Decision == idempotency.DecisionAcquired:
			acquired++
		case result.claim.AuditOperationConflict:
			auditConflicts++
		default:
			t.Fatalf("unexpected concurrent claim=%+v", result.claim)
		}
	}
	if acquired != 1 || auditConflicts != 1 {
		t.Fatalf("acquired=%d audit_conflicts=%d", acquired, auditConflicts)
	}
}

func TestReportExportPrepareReceiptReplayConflictIsolationAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report-export-restart.db")
	runtimeStore := openReportExportReceiptRuntimeStore(t, path)
	receipts := NewReportExportPrepareReceiptStore(runtimeStore)
	now := time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)
	request := reportExportPrepareClaimFixture("workspace-a", "requester-a", "audit-a", "caller-a", "fingerprint-a", now)

	claim, err := receipts.TryBeginReportExportPrepare(t.Context(), request)
	if err != nil || claim.Decision != idempotency.DecisionAcquired {
		t.Fatalf("initial claim=%+v err=%v", claim, err)
	}
	payload := `{"workspace_id":"workspace-a","requester_user_id":"requester-a","report_key":"revenue","object_key":"customer","audit_id":"audit-a","prepare_receipt_id":"` + claim.Receipt.ID + `"}`
	receipt, err := receipts.SaveReportExportPreparePayload(t.Context(), reportmodel.ReportExportPreparePayload{
		WorkspaceID: "workspace-a", ReceiptID: claim.Receipt.ID, PayloadJSON: payload, BusinessJobKey: "business-a",
		LeaseOwner: claim.Receipt.LeaseOwner, FencingToken: claim.Receipt.FencingToken, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = receipts.CompleteReportExportPrepare(t.Context(), reportmodel.ReportExportPrepareCompletion{
		WorkspaceID: "workspace-a", ReceiptID: receipt.ID, JobID: "job-a", LeaseOwner: receipt.LeaseOwner,
		FencingToken: receipt.FencingToken, Now: now, ExpiresAt: now.Add(24 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	replay, err := receipts.TryBeginReportExportPrepare(t.Context(), request)
	if err != nil || replay.Decision != idempotency.DecisionReplay || replay.Receipt.JobID != "job-a" {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
	changed := request
	changed.RequestFingerprint = "fingerprint-b"
	if conflict, conflictErr := receipts.TryBeginReportExportPrepare(t.Context(), changed); conflictErr != nil || conflict.Decision != idempotency.DecisionFingerprintConflict || conflict.AuditOperationConflict {
		t.Fatalf("fingerprint conflict=%+v err=%v", conflict, conflictErr)
	}
	otherCaller := request
	otherCaller.Receipt.CallerKey = "caller-b"
	if conflict, conflictErr := receipts.TryBeginReportExportPrepare(t.Context(), otherCaller); conflictErr != nil || !conflict.AuditOperationConflict {
		t.Fatalf("audit operation conflict=%+v err=%v", conflict, conflictErr)
	}
	otherWorkspace := reportExportPrepareClaimFixture("workspace-b", "requester-a", "audit-a", "caller-a", "fingerprint-a", now)
	if isolated, isolatedErr := receipts.TryBeginReportExportPrepare(t.Context(), otherWorkspace); isolatedErr != nil || isolated.Decision != idempotency.DecisionAcquired || isolated.Receipt.ID == claim.Receipt.ID {
		t.Fatalf("workspace isolation=%+v err=%v", isolated, isolatedErr)
	}
	otherRequester := reportExportPrepareClaimFixture("workspace-a", "requester-b", "audit-b", "caller-a", "fingerprint-a", now)
	if isolated, isolatedErr := receipts.TryBeginReportExportPrepare(t.Context(), otherRequester); isolatedErr != nil || isolated.Decision != idempotency.DecisionAcquired || isolated.Receipt.ID == claim.Receipt.ID {
		t.Fatalf("requester isolation=%+v err=%v", isolated, isolatedErr)
	}

	if err = runtimeStore.Close(); err != nil {
		t.Fatal(err)
	}
	restarted := openReportExportReceiptRuntimeStore(t, path)
	defer restarted.Close()
	stored, found, err := NewReportExportPrepareReceiptStore(restarted).GetReportExportPrepareReceipt(t.Context(), "workspace-a", claim.Receipt.ID)
	if err != nil || !found || stored.JobID != "job-a" || stored.PayloadJSON != payload || stored.Status != string(idempotency.StatusSucceeded) {
		t.Fatalf("restarted receipt=%+v found=%v err=%v", stored, found, err)
	}
}

func TestReportExportCompletionRacesSubmitCompletionAndRejectsRebinding(t *testing.T) {
	runtimeStore := openReportExportReceiptRuntimeStore(t, filepath.Join(t.TempDir(), "report-export-completion-race.db"))
	defer runtimeStore.Close()
	receipts := NewReportExportPrepareReceiptStore(runtimeStore)
	now := time.Date(2026, 9, 7, 11, 0, 0, 0, time.UTC)
	claim, err := receipts.TryBeginReportExportPrepare(t.Context(), reportExportPrepareClaimFixture("workspace-a", "requester-a", "audit-a", "caller-a", "fingerprint-a", now))
	if err != nil {
		t.Fatal(err)
	}
	payload := `{"receipt":"` + claim.Receipt.ID + `"}`
	receipt, err := receipts.SaveReportExportPreparePayload(t.Context(), reportmodel.ReportExportPreparePayload{
		WorkspaceID: "workspace-a", ReceiptID: claim.Receipt.ID, PayloadJSON: payload, BusinessJobKey: "business-a",
		LeaseOwner: claim.Receipt.LeaseOwner, FencingToken: claim.Receipt.FencingToken, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	binding := reportmodel.ReportExportCompletionBinding{
		ReceiptID: receipt.ID, WorkspaceID: "workspace-a", RequesterUserID: "requester-a", ReportKey: "revenue", ObjectKey: "customer", AuditID: "audit-a",
		JobID: "job-a", ArtifactID: "artifact-a", PayloadJSON: payload, CompletionFingerprint: "completion-a", Now: now, ExpiresAt: now.Add(24 * time.Hour),
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	var completeErr, bindErr error
	var bound reportmodel.ReportExportCompletionBindingResult
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		completeErr = receipts.CompleteReportExportPrepare(t.Context(), reportmodel.ReportExportPrepareCompletion{
			WorkspaceID: "workspace-a", ReceiptID: receipt.ID, JobID: "job-a", LeaseOwner: receipt.LeaseOwner,
			FencingToken: receipt.FencingToken, Now: now, ExpiresAt: now.Add(24 * time.Hour),
		})
	}()
	go func() {
		defer wg.Done()
		<-start
		bound, bindErr = receipts.BindReportExportCompletion(t.Context(), binding)
	}()
	close(start)
	wg.Wait()
	if completeErr != nil || bindErr != nil || (bound.Decision != reportmodel.ReportExportCompletionBound && bound.Decision != reportmodel.ReportExportCompletionReplay) {
		t.Fatalf("complete_err=%v bind=%+v bind_err=%v", completeErr, bound, bindErr)
	}
	stored, found, err := receipts.GetReportExportPrepareReceipt(t.Context(), "workspace-a", receipt.ID)
	if err != nil || !found || stored.Status != string(idempotency.StatusSucceeded) || stored.JobID != "job-a" || stored.CompletionArtifactID != "artifact-a" {
		t.Fatalf("stored=%+v found=%v err=%v", stored, found, err)
	}
	replayed, err := receipts.BindReportExportCompletion(t.Context(), binding)
	if err != nil || replayed.Decision != reportmodel.ReportExportCompletionReplay {
		t.Fatalf("completion replay=%+v err=%v", replayed, err)
	}
	changedArtifact := binding
	changedArtifact.ArtifactID = "artifact-b"
	changedArtifact.CompletionFingerprint = "completion-b"
	if conflict, conflictErr := receipts.BindReportExportCompletion(t.Context(), changedArtifact); conflictErr != nil || conflict.Decision != reportmodel.ReportExportCompletionConflict {
		t.Fatalf("artifact conflict=%+v err=%v", conflict, conflictErr)
	}
	changedJob := binding
	changedJob.JobID = "job-b"
	if conflict, conflictErr := receipts.BindReportExportCompletion(t.Context(), changedJob); conflictErr != nil || conflict.Decision != reportmodel.ReportExportCompletionConflict {
		t.Fatalf("job conflict=%+v err=%v", conflict, conflictErr)
	}
}

func TestReportExportPrepareExpiredLeaseReclaimsFrozenPayloadWithFencing(t *testing.T) {
	runtimeStore := openReportExportReceiptRuntimeStore(t, filepath.Join(t.TempDir(), "report-export-lease.db"))
	defer runtimeStore.Close()
	receipts := NewReportExportPrepareReceiptStore(runtimeStore)
	now := time.Date(2026, 9, 7, 12, 30, 0, 0, time.UTC)
	request := reportExportPrepareClaimFixture("workspace-a", "requester-a", "audit-a", "caller-a", "fingerprint-a", now)
	first, err := receipts.TryBeginReportExportPrepare(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	payload := `{"receipt":"frozen"}`
	if _, err = receipts.SaveReportExportPreparePayload(t.Context(), reportmodel.ReportExportPreparePayload{
		WorkspaceID: "workspace-a", ReceiptID: first.Receipt.ID, PayloadJSON: payload, BusinessJobKey: "business-a",
		LeaseOwner: first.Receipt.LeaseOwner, FencingToken: first.Receipt.FencingToken, Now: now,
	}); err != nil {
		t.Fatal(err)
	}
	live := request
	live.LeaseOwner, live.Now = "lease-owner-b", now.Add(30*time.Second)
	if claim, claimErr := receipts.TryBeginReportExportPrepare(t.Context(), live); claimErr != nil || claim.Decision != idempotency.DecisionInProgress {
		t.Fatalf("live lease claim=%+v err=%v", claim, claimErr)
	}
	expired := live
	expired.Now = now.Add(2 * time.Minute)
	reclaimed, err := receipts.TryBeginReportExportPrepare(t.Context(), expired)
	if err != nil || reclaimed.Decision != idempotency.DecisionAcquired || reclaimed.Receipt.FencingToken != first.Receipt.FencingToken+1 || reclaimed.Receipt.PayloadJSON != payload {
		t.Fatalf("reclaimed=%+v err=%v", reclaimed, err)
	}
	if err = receipts.CompleteReportExportPrepare(t.Context(), reportmodel.ReportExportPrepareCompletion{
		WorkspaceID: "workspace-a", ReceiptID: first.Receipt.ID, JobID: "stale-job", LeaseOwner: first.Receipt.LeaseOwner,
		FencingToken: first.Receipt.FencingToken, Now: expired.Now, ExpiresAt: expired.Now.Add(time.Hour),
	}); err == nil {
		t.Fatal("stale lease completed the reclaimed receipt")
	}
	if err = receipts.CompleteReportExportPrepare(t.Context(), reportmodel.ReportExportPrepareCompletion{
		WorkspaceID: "workspace-a", ReceiptID: reclaimed.Receipt.ID, JobID: "job-a", LeaseOwner: reclaimed.Receipt.LeaseOwner,
		FencingToken: reclaimed.Receipt.FencingToken, Now: expired.Now, ExpiresAt: expired.Now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
}

func TestExpiredEmptyReportExportPrepareOrphanCanBeAdoptedButFrozenWinnerCannot(t *testing.T) {
	runtimeStore := openReportExportReceiptRuntimeStore(t, filepath.Join(t.TempDir(), "report-export-orphan-adoption.db"))
	defer runtimeStore.Close()
	receipts := NewReportExportPrepareReceiptStore(runtimeStore)
	now := time.Date(2026, 9, 7, 13, 0, 0, 0, time.UTC)
	attacker := reportExportPrepareClaimFixture("workspace-a", "not-the-audit-owner", "audit-a", "attacker-key", "attacker-fingerprint", now)
	orphan, err := receipts.TryBeginReportExportPrepare(t.Context(), attacker)
	if err != nil || orphan.Decision != idempotency.DecisionAcquired {
		t.Fatalf("orphan claim=%+v err=%v", orphan, err)
	}

	owner := reportExportPrepareClaimFixture("workspace-a", "requester-a", "audit-a", "owner-key", "owner-fingerprint", now.Add(30*time.Second))
	if blocked, blockedErr := receipts.TryBeginReportExportPrepare(t.Context(), owner); blockedErr != nil || !blocked.AuditOperationConflict {
		t.Fatalf("live orphan must remain fenced: claim=%+v err=%v", blocked, blockedErr)
	}
	owner.Now = now.Add(2 * time.Minute)
	adopted, err := receipts.TryBeginReportExportPrepare(t.Context(), owner)
	if err != nil || adopted.Decision != idempotency.DecisionAcquired || adopted.Receipt.ID == orphan.Receipt.ID || adopted.Receipt.RequesterUserID != "requester-a" || adopted.Receipt.FencingToken != orphan.Receipt.FencingToken+1 {
		t.Fatalf("adopted=%+v err=%v", adopted, err)
	}
	payload := `{"receipt":"owned-and-frozen"}`
	frozen, err := receipts.SaveReportExportPreparePayload(t.Context(), reportmodel.ReportExportPreparePayload{
		WorkspaceID: "workspace-a", ReceiptID: adopted.Receipt.ID, PayloadJSON: payload, BusinessJobKey: "business-owner",
		LeaseOwner: adopted.Receipt.LeaseOwner, FencingToken: adopted.Receipt.FencingToken, Now: owner.Now,
	})
	if err != nil {
		t.Fatal(err)
	}

	challenger := reportExportPrepareClaimFixture("workspace-a", "requester-b", "audit-a", "challenger-key", "challenger-fingerprint", owner.Now.Add(2*time.Minute))
	if blocked, blockedErr := receipts.TryBeginReportExportPrepare(t.Context(), challenger); blockedErr != nil || !blocked.AuditOperationConflict || blocked.Receipt.ID != frozen.ID {
		t.Fatalf("frozen winner was adopted: claim=%+v err=%v", blocked, blockedErr)
	}
	if err = receipts.CompleteReportExportPrepare(t.Context(), reportmodel.ReportExportPrepareCompletion{
		WorkspaceID: "workspace-a", ReceiptID: frozen.ID, JobID: "job-owner", LeaseOwner: frozen.LeaseOwner,
		FencingToken: frozen.FencingToken, Now: owner.Now, ExpiresAt: owner.Now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
}

func TestReportExportPrepareFailuresAreRetainedAndTerminalFailureReplays(t *testing.T) {
	runtimeStore := openReportExportReceiptRuntimeStore(t, filepath.Join(t.TempDir(), "report-export-failure-retention.db"))
	defer runtimeStore.Close()
	receipts := NewReportExportPrepareReceiptStore(runtimeStore)
	now := time.Date(2026, 9, 7, 13, 30, 0, 0, time.UTC)

	terminalRequest := reportExportPrepareClaimFixture("workspace-a", "requester-a", "audit-terminal", "caller-terminal", "fingerprint-terminal", now)
	terminal, err := receipts.TryBeginReportExportPrepare(t.Context(), terminalRequest)
	if err != nil {
		t.Fatal(err)
	}
	terminalReceipt, err := receipts.SaveReportExportPreparePayload(t.Context(), reportmodel.ReportExportPreparePayload{
		WorkspaceID: "workspace-a", ReceiptID: terminal.Receipt.ID, PayloadJSON: `{"terminal":true}`, BusinessJobKey: "business-terminal",
		LeaseOwner: terminal.Receipt.LeaseOwner, FencingToken: terminal.Receipt.FencingToken, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	terminalExpiry := now.Add(90 * 24 * time.Hour)
	if err = receipts.FailReportExportPrepareTerminal(t.Context(), reportmodel.ReportExportPrepareFailure{
		WorkspaceID: "workspace-a", ReceiptID: terminalReceipt.ID, LeaseOwner: terminalReceipt.LeaseOwner,
		FencingToken: terminalReceipt.FencingToken, ErrorCode: idempotency.ErrorCodeKeyReused, Now: now, ExpiresAt: terminalExpiry,
	}); err != nil {
		t.Fatal(err)
	}
	replayed, err := receipts.TryBeginReportExportPrepare(t.Context(), terminalRequest)
	if err != nil || replayed.Decision != idempotency.DecisionReplay || replayed.Receipt.Status != string(idempotency.StatusFailedTerminal) ||
		replayed.Receipt.TerminalErrorCode != idempotency.ErrorCodeKeyReused || replayed.Receipt.ExpiresAt != terminalExpiry.Format(time.RFC3339Nano) {
		t.Fatalf("terminal replay=%+v err=%v", replayed, err)
	}

	retryRequest := reportExportPrepareClaimFixture("workspace-a", "requester-a", "audit-retry", "caller-retry", "fingerprint-retry", now)
	retry, err := receipts.TryBeginReportExportPrepare(t.Context(), retryRequest)
	if err != nil {
		t.Fatal(err)
	}
	retryReceipt, err := receipts.SaveReportExportPreparePayload(t.Context(), reportmodel.ReportExportPreparePayload{
		WorkspaceID: "workspace-a", ReceiptID: retry.Receipt.ID, PayloadJSON: `{"retry":true}`, BusinessJobKey: "business-retry",
		LeaseOwner: retry.Receipt.LeaseOwner, FencingToken: retry.Receipt.FencingToken, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	retryExpiry := now.Add(24 * time.Hour)
	if err = receipts.FailReportExportPrepareRetryable(t.Context(), reportmodel.ReportExportPrepareFailure{
		WorkspaceID: "workspace-a", ReceiptID: retryReceipt.ID, LeaseOwner: retryReceipt.LeaseOwner,
		FencingToken: retryReceipt.FencingToken, ErrorCode: "backend.report.export_submit_uncertain", Now: now, ExpiresAt: retryExpiry,
	}); err != nil {
		t.Fatal(err)
	}
	retryRequest.LeaseOwner = "retry-owner-2"
	retryRequest.Now = now.Add(time.Minute)
	reclaimed, err := receipts.TryBeginReportExportPrepare(t.Context(), retryRequest)
	if err != nil || reclaimed.Decision != idempotency.DecisionAcquired || reclaimed.Receipt.Status != string(idempotency.StatusProcessing) || reclaimed.Receipt.ExpiresAt != "" || reclaimed.Receipt.TerminalErrorCode != "" {
		t.Fatalf("retryable reclaim=%+v err=%v", reclaimed, err)
	}
}

func TestReportExportPrepareReceiptExactLookupBeyondFirstHundred(t *testing.T) {
	runtimeStore := openReportExportReceiptRuntimeStore(t, filepath.Join(t.TempDir(), "report-export-many-receipts.db"))
	defer runtimeStore.Close()
	receipts := NewReportExportPrepareReceiptStore(runtimeStore)
	now := time.Date(2026, 9, 7, 14, 0, 0, 0, time.UTC)
	var lastRequest reportmodel.ReportExportPrepareClaimRequest
	var lastReceipt reportmodel.ReportExportPrepareReceipt
	for index := 0; index < 128; index++ {
		request := reportExportPrepareClaimFixture(
			"workspace-a",
			"requester-a",
			fmt.Sprintf("audit-%03d", index),
			"shared-caller-key",
			fmt.Sprintf("fingerprint-%03d", index),
			now.Add(time.Duration(index)*time.Second),
		)
		claim, err := receipts.TryBeginReportExportPrepare(t.Context(), request)
		if err != nil || claim.Decision != idempotency.DecisionAcquired {
			t.Fatalf("claim %d=%+v err=%v", index, claim, err)
		}
		payload := fmt.Sprintf(`{"receipt_id":%q}`, claim.Receipt.ID)
		stored, err := receipts.SaveReportExportPreparePayload(t.Context(), reportmodel.ReportExportPreparePayload{
			WorkspaceID: request.Receipt.WorkspaceID, ReceiptID: claim.Receipt.ID, PayloadJSON: payload,
			BusinessJobKey: fmt.Sprintf("business-%03d", index), LeaseOwner: claim.Receipt.LeaseOwner,
			FencingToken: claim.Receipt.FencingToken, Now: request.Now,
		})
		if err != nil {
			t.Fatalf("save payload %d: %v", index, err)
		}
		if err = receipts.CompleteReportExportPrepare(t.Context(), reportmodel.ReportExportPrepareCompletion{
			WorkspaceID: request.Receipt.WorkspaceID, ReceiptID: stored.ID, JobID: fmt.Sprintf("job-%03d", index),
			LeaseOwner: stored.LeaseOwner, FencingToken: stored.FencingToken, Now: request.Now, ExpiresAt: request.Now.Add(time.Hour),
		}); err != nil {
			t.Fatalf("complete %d: %v", index, err)
		}
		lastRequest, lastReceipt = request, stored
	}

	found, ok, err := receipts.GetReportExportPrepareReceipt(t.Context(), "workspace-a", lastReceipt.ID)
	if err != nil || !ok || found.JobID != "job-127" {
		t.Fatalf("exact lookup=%+v found=%v err=%v", found, ok, err)
	}
	replayed, err := receipts.TryBeginReportExportPrepare(t.Context(), lastRequest)
	if err != nil || replayed.Decision != idempotency.DecisionReplay || replayed.Receipt.ID != lastReceipt.ID || replayed.Receipt.JobID != "job-127" {
		t.Fatalf("exact replay=%+v err=%v", replayed, err)
	}
}

func reportExportPrepareClaimFixture(workspaceID, requesterID, auditID, callerKey, fingerprint string, now time.Time) reportmodel.ReportExportPrepareClaimRequest {
	return reportmodel.ReportExportPrepareClaimRequest{
		Receipt: reportmodel.ReportExportPrepareReceipt{
			WorkspaceID: workspaceID, RequesterUserID: requesterID, UseCase: reportmodel.ReportExportPrepareUseCase,
			ReportKey: "revenue", ObjectKey: "customer", AuditID: auditID, CallerKey: callerKey,
		},
		RequestFingerprint: fingerprint, LeaseOwner: "lease-owner", LeaseTTL: time.Minute, Now: now,
	}
}

func openReportExportReceiptRuntimeStore(t testing.TB, path string) *database.RuntimeStore {
	t.Helper()
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: path, IntegrationSecretKey: "report-export-receipts"})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.EnsureRuntimeSchema(t.Context()); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	return store
}
