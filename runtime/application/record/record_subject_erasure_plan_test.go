package record_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	lifecyclemodel "github.com/domainry/domainry-lifecycle-sdk/model"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	appschema "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/appschema"
	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	"github.com/domainry/domainry-runtime/testsupport/lifecyclesdkfixture"
)

type failingPreparedFileStore struct {
	lifecyclecontract.SubjectArtifactStore
	fail bool
}

func (s *failingPreparedFileStore) DeleteSubjectFileVersion(ctx context.Context, ref lifecyclecontract.SubjectFileReference, evidence lifecyclecontract.SubjectFileEvidence) (lifecyclecontract.SubjectFileEvidence, error) {
	if s.fail {
		s.fail = false
		return lifecyclecontract.SubjectFileEvidence{}, errors.New("temporary file delete failure")
	}
	return s.SubjectArtifactStore.(lifecyclecontract.SubjectFileVersionDeleter).DeleteSubjectFileVersion(ctx, ref, evidence)
}

func TestPreparedRecordErasureRollsBackSQLAndResumesFilesFromFrozenPlan(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "erasure.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	object := definitionmodel.ObjectSchema{Key: "personal_document", Fields: []definitionmodel.FieldSchema{
		{Key: "subject", Type: "user", Config: map[string]any{"lifecycle_erase": "retain"}},
		{Key: "email", Type: "email", Config: map[string]any{"lifecycle_erase": "anonymize"}},
		{Key: "attachment", Type: recordmodel.RecordFileFieldType, Config: map[string]any{"lifecycle_subject_file": true, "lifecycle_erase": "delete", "scan_required": false}},
	}}
	objects := []definitionmodel.ObjectSchema{object}
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	manifest := manifestmodel.ManifestSchema{TemplateID: "erasure", Version: "1", Name: "Erasure", Objects: objects}
	if err := appschema.NewApplicationSchemaStore(store).SyncManifest(t.Context(), principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "erasure test"), manifest); err != nil {
		t.Fatal(err)
	}
	repository := recordpersistence.NewRecordStore(store)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	insert := func(id, subject, file, workspace string) {
		t.Helper()
		if err := repository.InsertRecord(t.Context(), workspace, object, recordmodel.Record{ID: id, CreatedAt: now, UpdatedAt: now, Data: map[string]any{"subject": subject, "email": subject + "@example.test", "attachment": subjectLifecycleStructuredFile(file)}}); err != nil {
			t.Fatal(err)
		}
	}
	insert("one", "subject-one", "private.txt", "workspace-one")
	insert("two", "subject-one", "shared.txt", "workspace-one")
	insert("other", "subject-other", "shared.txt", "workspace-one")
	insert("one", "subject-one", "private.txt", "workspace-other")
	root := t.TempDir()
	digest := sha256.Sum256([]byte("workspace-one"))
	directory := filepath.Join(root, "workspace-"+hex.EncodeToString(digest[:16]))
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"private.txt", "shared.txt"} {
		if err := os.WriteFile(filepath.Join(directory, file), []byte("PRIVATE FILE CONTENT"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	binding, err := lifecyclesdkfixture.Open(t.Context(), store, "erasure-test")
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := binding.SubjectArtifacts(root)
	if err != nil {
		t.Fatal(err)
	}
	// Source registry and scan facts must disappear with a private file.
	fileDigest := sha256.Sum256([]byte("PRIVATE FILE CONTENT"))
	for _, file := range []string{"private.txt", "shared.txt"} {
		if _, err := store.DB().ExecContext(t.Context(), `INSERT INTO _lifecycle_file_artifacts (id,workspace_id,object_key,field_key,filename,content_type,sha256,size_bytes,status,created_at) VALUES (?,?,?,?,?,?,?,?,?,?)`, file, "workspace-one", object.Key, "attachment", file, "text/plain", hex.EncodeToString(fileDigest[:]), len("PRIVATE FILE CONTENT"), "active", now); err != nil {
			t.Fatal(err)
		}
	}
	files := &failingPreparedFileStore{SubjectArtifactStore: artifacts, fail: true}
	service := recordapplication.NewRecordSubjectLifecycleApplicationService(repository, objects, files)
	plan, err := service.PrepareSubjectErasure(t.Context(), "request-one", "workspace-one", "subject-one")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plan), "@example.test") || strings.Contains(string(plan), "PRIVATE FILE CONTENT") {
		t.Fatalf("plan copied personal data: %s", plan)
	}
	if _, err := service.ErasePreparedSubject(t.Context(), "request-one", "workspace-other", "subject-one", plan, nil); err == nil {
		t.Fatal("cross-workspace plan accepted")
	}
	if _, err := service.ErasePreparedSubject(t.Context(), "request-one", "workspace-one", "subject-one", plan, []lifecyclemodel.LegalHold{{ID: "hold"}}); err == nil {
		t.Fatal("legal hold ignored")
	}
	second, _, err := repository.GetRecord(t.Context(), "workspace-one", object, "two")
	if err != nil {
		t.Fatal(err)
	}
	second.UpdatedAt = "changed-after-plan"
	if err := repository.UpdateRecord(t.Context(), "workspace-one", object, second); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ErasePreparedSubject(t.Context(), "request-one", "workspace-one", "subject-one", plan, nil); err == nil {
		t.Fatal("stale row accepted")
	}
	first, _, err := repository.GetRecord(t.Context(), "workspace-one", object, "one")
	if err != nil || first.Data["email"] != "subject-one@example.test" || !reflect.DeepEqual(first.Data["attachment"], subjectLifecycleStructuredFile("private.txt")) {
		t.Fatalf("SQL batch failed to roll back: %+v %v", first, err)
	}
	second.UpdatedAt = now
	if err := repository.UpdateRecord(t.Context(), "workspace-one", object, second); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ErasePreparedSubject(t.Context(), "request-one", "workspace-one", "subject-one", plan, nil); err == nil || !strings.Contains(err.Error(), "temporary file") {
		t.Fatalf("expected resumable file failure: %v", err)
	}
	first, _, err = repository.GetRecord(t.Context(), "workspace-one", object, "one")
	if err != nil || first.Data["attachment"] != nil || first.Data["email"] == "subject-one@example.test" {
		t.Fatalf("business cleanup did not commit: %+v %v", first, err)
	}
	// Reconstruct the application to prove retry uses the persisted plan,
	// including file pointers that no longer exist in the business records.
	service = recordapplication.NewRecordSubjectLifecycleApplicationService(repository, objects, files)
	receipt, err := service.ErasePreparedSubject(t.Context(), "request-one", "workspace-one", "subject-one", plan, nil)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := service.ErasePreparedSubject(t.Context(), "request-one", "workspace-one", "subject-one", plan, nil)
	if err != nil || string(replayed) != string(receipt) {
		t.Fatalf("receipt replay differs: %s %s %v", receipt, replayed, err)
	}
	var result struct {
		Updated           int `json:"updated_records"`
		Deleted, Retained []lifecyclecontract.SubjectFileEvidence
	}
	var counts map[string]json.RawMessage
	if err := json.Unmarshal(receipt, &counts); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(counts["updated_records"], &result.Updated); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(counts["deleted_files"], &result.Deleted); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(counts["retained_shared_files"], &result.Retained); err != nil {
		t.Fatal(err)
	}
	if result.Updated != 2 || len(result.Deleted) != 1 || len(result.Retained) != 1 {
		t.Fatalf("incomplete receipt: %s", receipt)
	}
	if _, err := os.Stat(filepath.Join(directory, "private.txt")); !os.IsNotExist(err) {
		t.Fatalf("private file remains: %v", err)
	}
	if _, err := os.Stat(filepath.Join(directory, "shared.txt")); err != nil {
		t.Fatalf("shared file removed: %v", err)
	}
	var registryCount int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _lifecycle_file_artifacts WHERE workspace_id=? AND filename=?`, "workspace-one", "private.txt").Scan(&registryCount); err != nil || registryCount != 0 {
		t.Fatalf("private file registry remains: %d %v", registryCount, err)
	}
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _lifecycle_file_artifacts WHERE workspace_id=? AND filename=?`, "workspace-one", "shared.txt").Scan(&registryCount); err != nil || registryCount != 1 {
		t.Fatalf("shared file registry removed: %d %v", registryCount, err)
	}
	for _, target := range [][2]string{{"workspace-one", "other"}, {"workspace-other", "one"}} {
		record, _, err := repository.GetRecord(t.Context(), target[0], object, target[1])
		if err != nil || record.Data["attachment"] == nil || !strings.Contains(record.Data["email"].(string), "@example.test") {
			t.Fatalf("unrelated record changed: %+v %v", record, err)
		}
	}
}
