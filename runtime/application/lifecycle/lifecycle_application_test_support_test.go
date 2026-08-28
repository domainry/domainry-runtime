package lifecycle

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	lifecyclecontract "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/contract"
	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	localartifact "github.com/domainry/domainry-runtime/runtime/infrastructure/lifecycleartifact/filesystem"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	lifecyclepersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/lifecycle"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

type subjectResolverStub struct{}

func (subjectResolverStub) ResolveSubject(_ context.Context, workspaceID, subjectType, subjectID string) (string, error) {
	return workspaceID + ":" + subjectType + ":" + subjectID, nil
}

type subjectHandlerStub struct{ erased bool }

func (h *subjectHandlerStub) Owner(context.Context) string { return "identity" }
func (h *subjectHandlerStub) PreviewSubject(_ context.Context, _, identity string) (json.RawMessage, error) {
	return json.RawMessage(`{"identity":"` + identity + `","rows":1}`), nil
}
func (h *subjectHandlerStub) ExportSubject(_ context.Context, _, identity string) (json.RawMessage, error) {
	return json.RawMessage(`{"identity":"` + identity + `"}`), nil
}
func (h *subjectHandlerStub) EraseSubject(_ context.Context, _, _ string, _ []lifecyclemodel.LegalHold) (json.RawMessage, error) {
	h.erased = true
	return json.RawMessage(`{"anonymized":1}`), nil
}

func newLifecycleApplicationTestService(t *testing.T) (*LifecycleApplicationService, *database.RuntimeStore) {
	t.Helper()
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "lifecycle-application.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	executors := lifecyclepersistence.DefaultOwnerExecutors(store)
	ports := make([]lifecyclecontract.OwnerLifecycleExecutor, 0, len(executors))
	for index := range executors {
		ports = append(ports, executors[index])
	}
	service := NewLifecycleApplicationService(t.Context(), LifecycleApplicationDependencies{Repository: lifecyclepersistence.NewLifecycleStore(store), Executors: ports, SubjectResolver: subjectResolverStub{}, SubjectHandlers: []lifecyclecontract.SubjectDataHandler{&subjectHandlerStub{}}, Artifacts: localartifact.NewSubjectStore(t.TempDir())})
	return service, store
}

func lifecycleAdmin(workspaceID, userID string) principalmodel.Principal {
	return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: workspaceID, UserID: userID}}, accessfixture.Bundle{Key: "admin", Permissions: []string{
		"workspace.admin",
		PermissionPolicyManage,
		PermissionCleanupRun,
		PermissionSubjectManage,
	}})
}

func publishTestPolicy(t *testing.T, service *LifecycleApplicationService, admin principalmodel.Principal, now time.Time) lifecyclemodel.PolicyVersion {
	t.Helper()
	version, err := service.PublishPolicy(t.Context(), lifecyclemodel.PolicyVersion{Policy: lifecyclemodel.RetentionPolicy{Key: "integration.webhook_nonce.v1", Version: "1", Owner: "integration", Class: lifecyclemodel.RetentionClassTechnical, DefaultRetention: time.Hour, MinimumRetention: 15 * time.Minute, WorkspaceMayExtend: true, BackupBehavior: lifecyclemodel.BackupBehaviorStandard, EraseBehavior: lifecyclemodel.EraseBehaviorDelete}, Status: lifecyclemodel.PolicyStatusPublished, Revision: 1, PublishedAt: now}, admin)
	if err != nil {
		t.Fatal(err)
	}
	return version
}
