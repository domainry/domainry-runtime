package lifecycle

import (
	"context"
	"database/sql/driver"
	"os"
	"path/filepath"
	"testing"
	"time"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	lifecyclecontract "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/contract"
)

func artifactObjects() []definitionmodel.ObjectSchema {
	return []definitionmodel.ObjectSchema{
		{Key: "asset", Fields: []definitionmodel.FieldSchema{{Key: "file_url"}}},
		{Key: "download_task", Fields: []definitionmodel.FieldSchema{{Key: "token_status"}, {Key: "file_name"}, {Key: "status"}}},
	}
}

func scriptedArtifactStore(t *testing.T, state *lifecycleSQLState, objects []definitionmodel.ObjectSchema) *FileArtifactStore {
	t.Helper()
	s := NewFileArtifactStore(openLifecycleStore(t), objects, t.TempDir())
	s.db = openLifecycleScriptedDB(state)
	t.Cleanup(func() { _ = s.db.Close() })
	return s
}

func validArtifact(now time.Time) lifecyclecontract.UploadArtifact {
	return lifecyclecontract.UploadArtifact{WorkspaceID: "workspace-a", ObjectKey: "asset", FieldKey: "file_url", Filename: "a.txt", SHA256: "hash", Size: 1, CreatedAt: now}
}

func TestFileArtifactRegisterValidationAndSQLFailures(t *testing.T) {
	now := time.Date(2026, 7, 20, 1, 0, 0, 0, time.UTC)
	var nilStore *FileArtifactStore
	if err := nilStore.RegisterUpload(t.Context(), validArtifact(now)); err == nil {
		t.Fatal("expected unavailable error")
	}
	if err := (&FileArtifactStore{}).RegisterUpload(t.Context(), validArtifact(now)); err == nil {
		t.Fatal("expected missing store error")
	}
	for _, artifact := range []lifecyclecontract.UploadArtifact{
		{WorkspaceID: ""},
		{WorkspaceID: "workspace-a", ObjectKey: "missing", FieldKey: "x", Filename: "a", SHA256: "h", CreatedAt: now},
		{WorkspaceID: "workspace-a", ObjectKey: "asset", FieldKey: "missing", Filename: "a", SHA256: "h", CreatedAt: now},
		{WorkspaceID: "workspace-a", ObjectKey: "asset", FieldKey: "file_url", Filename: "", SHA256: "h", CreatedAt: now},
		{WorkspaceID: "workspace-a", ObjectKey: "asset", FieldKey: "file_url", Filename: "../a", SHA256: "h", CreatedAt: now},
		{WorkspaceID: "workspace-a", ObjectKey: "asset", FieldKey: "file_url", Filename: "a", SHA256: "", CreatedAt: now},
		{WorkspaceID: "workspace-a", ObjectKey: "asset", FieldKey: "file_url", Filename: "a", SHA256: "h", Size: -1, CreatedAt: now},
		{WorkspaceID: "workspace-a", ObjectKey: "asset", FieldKey: "file_url", Filename: "a", SHA256: "h"},
	} {
		if err := scriptedArtifactStore(t, &lifecycleSQLState{}, artifactObjects()).RegisterUpload(t.Context(), artifact); err == nil {
			t.Fatalf("expected validation error: %+v", artifact)
		}
	}
	for _, state := range []*lifecycleSQLState{
		{querySteps: []lifecycleSQLQueryStep{{err: errLifecycleSQL}}},
		{querySteps: []lifecycleSQLQueryStep{{columns: []string{"id"}, rows: [][]driver.Value{{"id"}}}}, execSteps: []lifecycleSQLExecStep{{err: errLifecycleSQL}}},
		{querySteps: []lifecycleSQLQueryStep{{}}, execSteps: []lifecycleSQLExecStep{{err: errLifecycleSQL}}},
	} {
		if err := scriptedArtifactStore(t, state, artifactObjects()).RegisterUpload(t.Context(), validArtifact(now)); err == nil {
			t.Fatal("expected SQL error")
		}
	}
	for _, state := range []*lifecycleSQLState{
		{querySteps: []lifecycleSQLQueryStep{{columns: []string{"id"}, rows: [][]driver.Value{{"id"}}}}},
		{querySteps: []lifecycleSQLQueryStep{{}}},
	} {
		if err := scriptedArtifactStore(t, state, artifactObjects()).RegisterUpload(t.Context(), validArtifact(now)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFileArtifactReconcileFailures(t *testing.T) {
	now := time.Date(2026, 7, 20, 1, 0, 0, 0, time.UTC)
	var nilStore *FileArtifactStore
	if _, err := nilStore.ReconcileUploadArtifacts(t.Context(), now, 1); err == nil {
		t.Fatal("expected unavailable error")
	}
	if _, err := (&FileArtifactStore{}).ReconcileUploadArtifacts(t.Context(), now, 1); err == nil {
		t.Fatal("expected missing store error")
	}
	if _, err := scriptedArtifactStore(t, &lifecycleSQLState{querySteps: []lifecycleSQLQueryStep{{err: errLifecycleSQL}}}, artifactObjects()).ReconcileUploadArtifacts(t.Context(), now, 1); err == nil {
		t.Fatal("expected download expiration error")
	}
	if _, err := scriptedArtifactStore(t, &lifecycleSQLState{querySteps: []lifecycleSQLQueryStep{{}, {}}}, artifactObjects()).ReconcileUploadArtifacts(t.Context(), now, 1001); err != nil {
		t.Fatal(err)
	}

	// With no download_task schema the first query belongs to artifact candidates.
	assetOnly := artifactObjects()[:1]
	for _, step := range []lifecycleSQLQueryStep{
		{err: errLifecycleSQL},
		{columns: []string{"id"}, rows: [][]driver.Value{{"x"}}},
		{columns: []string{"id", "workspace_id", "object_key", "field_key", "filename", "status", "created_at", "delete_after"}, nextErr: errLifecycleSQL},
	} {
		if _, err := scriptedArtifactStore(t, &lifecycleSQLState{querySteps: []lifecycleSQLQueryStep{step}}, assetOnly).ReconcileUploadArtifacts(t.Context(), now, 0); err == nil {
			t.Fatal("expected candidate error")
		}
	}

	candidateColumns := []string{"id", "workspace_id", "object_key", "field_key", "filename", "status", "created_at", "delete_after"}
	candidate := func(status, workspace, filename, deleteAfter string) lifecycleSQLQueryStep {
		return lifecycleSQLQueryStep{columns: candidateColumns, rows: [][]driver.Value{{"id", workspace, "asset", "file_url", filename, status, "", deleteAfter}}}
	}
	for _, state := range []*lifecycleSQLState{
		{querySteps: []lifecycleSQLQueryStep{candidate("staged", "workspace-a", "a.txt", now.Add(time.Hour).Format(time.RFC3339Nano)), {err: errLifecycleSQL}}},
		{querySteps: []lifecycleSQLQueryStep{candidate("staged", "workspace-a", "a.txt", now.Add(time.Hour).Format(time.RFC3339Nano)), {columns: []string{"count"}, rows: [][]driver.Value{{int64(1)}}}}, execSteps: []lifecycleSQLExecStep{{err: errLifecycleSQL}}},
		{querySteps: []lifecycleSQLQueryStep{candidate("referenced", "workspace-a", "a.txt", ""), {columns: []string{"count"}, rows: [][]driver.Value{{int64(0)}}}}, execSteps: []lifecycleSQLExecStep{{err: errLifecycleSQL}}},
		{querySteps: []lifecycleSQLQueryStep{candidate("orphaned", "", "a.txt", now.Add(-time.Hour).Format(time.RFC3339Nano)), {columns: []string{"count"}, rows: [][]driver.Value{{int64(0)}}}}},
	} {
		if _, err := scriptedArtifactStore(t, state, assetOnly).ReconcileUploadArtifacts(t.Context(), now, 1); err == nil {
			t.Fatal("expected reconciliation error")
		}
	}

	state := &lifecycleSQLState{querySteps: []lifecycleSQLQueryStep{candidate("orphaned", "workspace-a", "a.txt", now.Add(-time.Hour).Format(time.RFC3339Nano)), {columns: []string{"count"}, rows: [][]driver.Value{{int64(0)}}}}}
	s := scriptedArtifactStore(t, state, assetOnly)
	s.removeFile = func(string) error { return errLifecycleSQL }
	if _, err := s.ReconcileUploadArtifacts(t.Context(), now, 1); err == nil {
		t.Fatal("expected remove error")
	}

	state = &lifecycleSQLState{querySteps: []lifecycleSQLQueryStep{candidate("orphaned", "workspace-a", "a.txt", now.Add(-time.Hour).Format(time.RFC3339Nano)), {columns: []string{"count"}, rows: [][]driver.Value{{int64(0)}}}}, execSteps: []lifecycleSQLExecStep{{err: errLifecycleSQL}}}
	s = scriptedArtifactStore(t, state, assetOnly)
	s.removeFile = func(string) error { return os.ErrNotExist }
	if _, err := s.ReconcileUploadArtifacts(t.Context(), now, 1); err == nil {
		t.Fatal("expected deleted-state error")
	}

	state = &lifecycleSQLState{querySteps: []lifecycleSQLQueryStep{candidate("staged", "workspace-a", "a.txt", "")}}
	s = scriptedArtifactStore(t, state, assetOnly)
	s.contextErr = func(context.Context) error { return context.Canceled }
	if _, err := s.ReconcileUploadArtifacts(t.Context(), now, 1); err == nil {
		t.Fatal("expected context error")
	}
}

func TestFileArtifactDownloadAndHelperEdges(t *testing.T) {
	now := time.Date(2026, 7, 20, 1, 0, 0, 0, time.UTC)
	for _, objects := range [][]definitionmodel.ObjectSchema{nil, {{Key: "download_task"}}, {{Key: "download_task", Fields: []definitionmodel.FieldSchema{{Key: "token_status"}}}}} {
		if count, err := scriptedArtifactStore(t, &lifecycleSQLState{}, objects).expireDownloadTasks(t.Context(), now, 1); err != nil || count != 0 {
			t.Fatalf("count=%d err=%v", count, err)
		}
	}
	missingStatus := []definitionmodel.ObjectSchema{{Key: "download_task", Fields: []definitionmodel.FieldSchema{{Key: "token_status"}, {Key: "file_name"}}}}
	if count, err := scriptedArtifactStore(t, &lifecycleSQLState{}, missingStatus).expireDownloadTasks(t.Context(), now, 1); err != nil || count != 0 {
		t.Fatal("missing status contract")
	}
	columns := []string{"workspace_id", "id"}
	for _, state := range []*lifecycleSQLState{
		{querySteps: []lifecycleSQLQueryStep{{err: errLifecycleSQL}}},
		{querySteps: []lifecycleSQLQueryStep{{columns: []string{"workspace_id"}, rows: [][]driver.Value{{"w"}}}}},
		{querySteps: []lifecycleSQLQueryStep{{columns: columns, nextErr: errLifecycleSQL}}},
		{querySteps: []lifecycleSQLQueryStep{{columns: columns, rows: [][]driver.Value{{"w", "id"}}}}, execSteps: []lifecycleSQLExecStep{{err: errLifecycleSQL}}},
		{querySteps: []lifecycleSQLQueryStep{{columns: columns, rows: [][]driver.Value{{"w", "id"}}}}, execSteps: []lifecycleSQLExecStep{{rowsErr: errLifecycleSQL}}},
	} {
		if _, err := scriptedArtifactStore(t, state, artifactObjects()).expireDownloadTasks(t.Context(), now, 1); err == nil {
			t.Fatal("expected download expiration error")
		}
	}
	state := &lifecycleSQLState{querySteps: []lifecycleSQLQueryStep{{columns: columns, rows: [][]driver.Value{{"w", "id"}}}}, execSteps: []lifecycleSQLExecStep{{rows: 2}}}
	if count, err := scriptedArtifactStore(t, state, artifactObjects()).expireDownloadTasks(t.Context(), now, 1); err != nil || count != 2 {
		t.Fatalf("count=%d err=%v", count, err)
	}

	s := scriptedArtifactStore(t, &lifecycleSQLState{querySteps: []lifecycleSQLQueryStep{{err: errLifecycleSQL}}}, artifactObjects())
	if _, err := s.artifactReferenced(t.Context(), "w", "asset", "file_url", "a"); err == nil {
		t.Fatal("expected reference error")
	}
	if referenced, err := s.artifactReferenced(t.Context(), "w", "missing", "x", "a"); err != nil || referenced {
		t.Fatalf("referenced=%v err=%v", referenced, err)
	}
	if referenced, err := s.artifactReferenced(t.Context(), "w", "asset", "missing", "a"); err != nil || referenced {
		t.Fatal("unexpected missing field reference")
	}

	for _, tc := range []struct{ workspace, filename, root string }{{"", "a", "/tmp"}, {"workspace-a", "../a", "/tmp"}, {"workspace-a", "a", ""}} {
		s := scriptedArtifactStore(t, &lifecycleSQLState{}, nil)
		s.uploadRoot = tc.root
		if _, err := s.artifactPath(tc.workspace, tc.filename); err == nil {
			t.Fatal("expected path error")
		}
	}
	s = scriptedArtifactStore(t, &lifecycleSQLState{}, nil)
	if _, err := s.artifactPath("workspace-a", ""); err == nil {
		t.Fatal("expected empty filename error")
	}
	s = scriptedArtifactStore(t, &lifecycleSQLState{}, nil)
	s.absPath = func(string) (string, error) { return "", errLifecycleSQL }
	if _, err := s.artifactPath("workspace-a", "a"); err == nil {
		t.Fatal("expected abs error")
	}
	s = scriptedArtifactStore(t, &lifecycleSQLState{}, nil)
	s.relPath = func(string, string) (string, error) { return "", errLifecycleSQL }
	if _, err := s.artifactPath("workspace-a", "a"); err == nil {
		t.Fatal("expected rel error")
	}
	s = scriptedArtifactStore(t, &lifecycleSQLState{}, nil)
	s.relPath = func(string, string) (string, error) { return filepath.Join("..", "escape"), nil }
	if _, err := s.artifactPath("workspace-a", "a"); err == nil {
		t.Fatal("expected escape error")
	}
	s = scriptedArtifactStore(t, &lifecycleSQLState{}, nil)
	absolutePath := filepath.Join(filepath.VolumeName(os.TempDir())+string(filepath.Separator), "absolute")
	s.relPath = func(string, string) (string, error) { return absolutePath, nil }
	if _, err := s.artifactPath("workspace-a", "a"); err == nil {
		t.Fatal("expected absolute relative path error")
	}
	if lifecycleObjectHasField(definitionmodel.ObjectSchema{}, "x") {
		t.Fatal("unexpected field")
	}
}
