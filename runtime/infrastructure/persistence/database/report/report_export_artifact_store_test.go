package report

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func validReportExportArtifact() reportmodel.ReportExportArtifact {
	return reportmodel.ReportExportArtifact{
		WorkspaceID: "workspace-a", ReportKey: "revenue", ObjectKey: "order", AuditID: "audit-1",
		RequesterUserID: "operator-1", RoleKey: "finance", IdempotencyKey: "prepare-1", Token: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Filename: "revenue-order.csv", Scope: reportmodel.ReportExportScopeRequest{Purpose: "month close", Freshness: reportmodel.ReportExportFreshness{Mode: "realtime"}},
		ScopeSHA256: "scope-a", AuthorizationScopeSHA256: "auth-a", ReportDefinitionSHA256: "report-a", ControlDefinitionSHA256: "control-a",
		ContentSHA256: "content-a", RowCount: 1, Content: []byte("total\n10\n"), CreatedAt: "2026-08-10T10:00:00Z", ExpiresAt: "2026-08-10T10:15:00Z",
	}
}

func TestReportExportArtifactStoreIsConcurrentIdempotentAndRestartDurable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "exports.db")
	open := func() *database.RuntimeStore {
		store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: path, IntegrationSecretKey: "export-test-key"})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
			_ = store.Close()
			t.Fatal(err)
		}
		return store
	}
	store := open()
	repository := NewReportExportArtifactStore(store)
	artifact := validReportExportArtifact()
	var created atomic.Int64
	var wait sync.WaitGroup
	errors := make(chan error, 16)
	for range 16 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			result, wasCreated, err := repository.CreateOrGetReportExportArtifact(t.Context(), artifact)
			if err == nil && result.ID == "" {
				err = reportcontract.ErrReportExportIdempotencyConflict
			}
			if wasCreated {
				created.Add(1)
			}
			errors <- err
		}()
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	if created.Load() != 1 {
		t.Fatalf("created=%d", created.Load())
	}
	conflict := artifact
	conflict.ScopeSHA256 = "scope-b"
	if _, _, err := repository.CreateOrGetReportExportArtifact(t.Context(), conflict); err != reportcontract.ErrReportExportIdempotencyConflict {
		t.Fatalf("conflict err=%v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store = open()
	t.Cleanup(func() { _ = store.Close() })
	restarted := NewReportExportArtifactStore(store)
	result, found, err := restarted.ReportExportArtifactByToken(t.Context(), artifact.WorkspaceID, artifact.Token)
	if err != nil || !found || result.Scope.Purpose != artifact.Scope.Purpose || string(result.Content) != string(artifact.Content) || result.RowCount != 1 {
		t.Fatalf("result=%#v found=%v err=%v", result, found, err)
	}
	if err := restarted.LinkReportExportBusinessDownload(t.Context(), artifact.WorkspaceID, result.ID, "download-1"); err != nil {
		t.Fatal(err)
	}
	result, found, err = restarted.ReportExportArtifactByToken(t.Context(), artifact.WorkspaceID, artifact.Token)
	if err != nil || !found || result.BusinessDownloadID != "download-1" {
		t.Fatalf("linked result=%#v found=%v err=%v", result, found, err)
	}
	if _, found, err := restarted.ReportExportArtifactByToken(t.Context(), "other-workspace", artifact.Token); err != nil || found {
		t.Fatalf("cross workspace found=%v err=%v", found, err)
	}
}

func TestReportExportArtifactStoreRejectsInvalidConflictingAndCorruptState(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "edges.db"), IntegrationSecretKey: "export-test-key"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewReportExportArtifactStore(store)
	artifact := validReportExportArtifact()
	invalidWorkspace := artifact
	invalidWorkspace.WorkspaceID = ""
	if _, _, err := repository.CreateOrGetReportExportArtifact(t.Context(), invalidWorkspace); err == nil {
		t.Fatal("missing workspace accepted")
	}
	invalidIdentity := artifact
	invalidIdentity.ReportKey = ""
	if _, _, err := repository.CreateOrGetReportExportArtifact(t.Context(), invalidIdentity); err == nil {
		t.Fatal("missing artifact identity accepted")
	}
	for name, mutate := range map[string]func(*reportmodel.ReportExportArtifact){
		"object":       func(value *reportmodel.ReportExportArtifact) { value.ObjectKey = "" },
		"audit":        func(value *reportmodel.ReportExportArtifact) { value.AuditID = "" },
		"requester":    func(value *reportmodel.ReportExportArtifact) { value.RequesterUserID = "" },
		"idempotency":  func(value *reportmodel.ReportExportArtifact) { value.IdempotencyKey = "" },
		"token":        func(value *reportmodel.ReportExportArtifact) { value.Token = "" },
		"scope hash":   func(value *reportmodel.ReportExportArtifact) { value.ScopeSHA256 = "" },
		"content hash": func(value *reportmodel.ReportExportArtifact) { value.ContentSHA256 = "" },
		"row count":    func(value *reportmodel.ReportExportArtifact) { value.RowCount = -1 },
		"content":      func(value *reportmodel.ReportExportArtifact) { value.Content = nil },
	} {
		t.Run("missing "+name, func(t *testing.T) {
			invalid := artifact
			mutate(&invalid)
			if _, _, err := repository.CreateOrGetReportExportArtifact(t.Context(), invalid); err == nil {
				t.Fatal("invalid artifact accepted")
			}
		})
	}
	created, _, err := repository.CreateOrGetReportExportArtifact(t.Context(), artifact)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.LinkReportExportBusinessDownload(t.Context(), "", created.ID, "download"); err == nil {
		t.Fatal("empty link identity accepted")
	}
	if err := repository.LinkReportExportBusinessDownload(t.Context(), artifact.WorkspaceID, "", "download"); err == nil {
		t.Fatal("empty artifact link identity accepted")
	}
	if err := repository.LinkReportExportBusinessDownload(t.Context(), artifact.WorkspaceID, created.ID, ""); err == nil {
		t.Fatal("empty business download identity accepted")
	}
	if err := repository.LinkReportExportBusinessDownload(t.Context(), artifact.WorkspaceID, "missing", "download"); err == nil {
		t.Fatal("missing artifact link accepted")
	}
	if err := repository.LinkReportExportBusinessDownload(t.Context(), artifact.WorkspaceID, created.ID, "download-1"); err != nil {
		t.Fatal(err)
	}
	if err := repository.LinkReportExportBusinessDownload(t.Context(), artifact.WorkspaceID, created.ID, "download-1"); err != nil {
		t.Fatal(err)
	}
	if err := repository.LinkReportExportBusinessDownload(t.Context(), artifact.WorkspaceID, created.ID, "download-2"); err == nil {
		t.Fatal("conflicting business download link accepted")
	}
	conflictingRequest := artifact
	conflictingRequest.ScopeSHA256 = "other"
	if _, _, err := reconcileReportExportInsertConflict(created, conflictingRequest); err != reportcontract.ErrReportExportIdempotencyConflict {
		t.Fatalf("reconcile conflict err=%v", err)
	}
	if replay, createdAgain, err := reconcileReportExportInsertConflict(created, artifact); err != nil || createdAgain || replay.ID != created.ID {
		t.Fatalf("reconcile replay=%#v created=%v err=%v", replay, createdAgain, err)
	}
	collision := artifact
	collision.IdempotencyKey = "prepare-2"
	if _, _, err := repository.CreateOrGetReportExportArtifact(t.Context(), collision); err == nil {
		t.Fatal("duplicate token accepted")
	}
	table := store.TableIdentifier("report_export_artifacts")
	if _, err := store.DB().ExecContext(t.Context(), "UPDATE "+table+" SET scope_json = '{' WHERE id = ?", created.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.ReportExportArtifactByToken(t.Context(), artifact.WorkspaceID, artifact.Token); err == nil {
		t.Fatal("corrupt scope JSON accepted")
	}
	if _, err := store.DB().ExecContext(t.Context(), "UPDATE "+table+" SET scope_json = '{}', content_base64 = '%' WHERE id = ?", created.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.ReportExportArtifactByToken(t.Context(), artifact.WorkspaceID, artifact.Token); err == nil {
		t.Fatal("corrupt content encoding accepted")
	}
}

func TestReportExportArtifactInsertConflictReadFailure(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "conflict-read.db"), IntegrationSecretKey: "export-test-key"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewReportExportArtifactStore(store)
	artifact := validReportExportArtifact()
	if _, _, err := repository.CreateOrGetReportExportArtifact(t.Context(), artifact); err != nil {
		t.Fatal(err)
	}
	original := scanReportExportArtifact
	t.Cleanup(func() { scanReportExportArtifact = original })
	calls := 0
	scanReportExportArtifact = func(row *sql.Row) (reportmodel.ReportExportArtifact, bool, error) {
		calls++
		artifact, found, err := original(row)
		if calls == 1 {
			return artifact, found, err
		}
		return reportmodel.ReportExportArtifact{}, false, errors.New("conflict read failed")
	}
	collision := artifact
	collision.IdempotencyKey = "other"
	if _, _, err := repository.CreateOrGetReportExportArtifact(t.Context(), collision); err == nil {
		t.Fatal("insert conflict read failure was ignored")
	}
}

func TestReportExportArtifactInsertConflictReconcilesConcurrentWinner(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "conflict-winner.db"), IntegrationSecretKey: "export-test-key"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewReportExportArtifactStore(store)
	artifact := validReportExportArtifact()
	original := execReportExportArtifact
	execReportExportArtifact = func(ctx context.Context, db database.ActionExecutionExecutor, query string, args ...any) (sql.Result, error) {
		execReportExportArtifact = original
		result, insertErr := original(ctx, db, query, args...)
		if insertErr != nil {
			return result, insertErr
		}
		return result, errors.New("simulated losing concurrent insert")
	}
	t.Cleanup(func() { execReportExportArtifact = original })
	got, created, err := repository.CreateOrGetReportExportArtifact(t.Context(), artifact)
	if err != nil || created || got.IdempotencyKey != artifact.IdempotencyKey {
		t.Fatalf("artifact=%#v created=%v err=%v", got, created, err)
	}
}

func TestReportExportArtifactStorePropagatesClosedDatabaseFailures(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "closed.db"), IntegrationSecretKey: "export-test-key"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewReportExportArtifactStore(store)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	artifact := validReportExportArtifact()
	if _, _, err := repository.CreateOrGetReportExportArtifact(t.Context(), artifact); !errorsIsSQLClosed(err) {
		t.Fatalf("create err=%v", err)
	}
	if _, _, err := repository.ReportExportArtifactByToken(t.Context(), artifact.WorkspaceID, artifact.Token); !errorsIsSQLClosed(err) {
		t.Fatalf("read err=%v", err)
	}
	if err := repository.LinkReportExportBusinessDownload(t.Context(), artifact.WorkspaceID, "artifact", "download"); !errorsIsSQLClosed(err) {
		t.Fatalf("link err=%v", err)
	}
}

func TestReportExportCanonicalReplayDoesNotDependOnAuditIdentity(t *testing.T) {
	first := validReportExportArtifact()
	replay := first
	replay.AuditID = "audit-retry"
	if !sameReportExportRequest(first, replay) {
		t.Fatal("equivalent export under a later audit did not reuse the canonical artifact")
	}
	replay.ScopeSHA256 = "changed"
	if sameReportExportRequest(first, replay) {
		t.Fatal("changed export scope reused the canonical artifact")
	}
}

func errorsIsSQLClosed(err error) bool {
	return err != nil && (err == sql.ErrConnDone || strings.Contains(err.Error(), "closed"))
}
