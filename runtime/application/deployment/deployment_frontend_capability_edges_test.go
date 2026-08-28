package deployment

import (
	"context"
	"errors"
	"reflect"
	"testing"

	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

var errFrontendCapabilityRepository = errors.New("frontend capability repository failed")

type frontendCapabilityRepositoryStub struct {
	state    deploymentmodel.DeploymentFrontendCapabilityState
	found    bool
	getErr   error
	putErr   error
	putCalls int
}

func (r *frontendCapabilityRepositoryStub) Get(context.Context, string) (deploymentmodel.DeploymentFrontendCapabilityState, bool, error) {
	return r.state, r.found, r.getErr
}

func (r *frontendCapabilityRepositoryStub) Put(_ context.Context, workspaceID string, payload []byte) (deploymentmodel.DeploymentFrontendCapabilityState, error) {
	r.putCalls++
	if r.putErr != nil {
		return deploymentmodel.DeploymentFrontendCapabilityState{}, r.putErr
	}
	r.state = deploymentmodel.DeploymentFrontendCapabilityState{WorkspaceID: workspaceID, ManifestJSON: append([]byte(nil), payload...), Revision: 2, UpdatedAt: "now"}
	r.found = true
	return r.state, nil
}

func TestFrontendCapabilityConstructorAndMemoryFallback(t *testing.T) {
	t.Parallel()

	service := NewDeploymentFrontendCapabilityApplicationService()
	if service.contractVersion != "runtime-authoring-v1" || service.capabilities == nil || service.Configured() {
		t.Fatalf("default service=%#v", service)
	}
	if capabilities := service.capabilities(); capabilities != nil {
		t.Fatalf("default capabilities=%v", capabilities)
	}
	if (*DeploymentFrontendCapabilityApplicationService)(nil).Configured() {
		t.Fatal("nil service reported configured")
	}
	configured := NewDeploymentFrontendCapabilityApplicationService(FrontendCapabilityDependencies{Repository: &frontendCapabilityRepositoryStub{}, ContractVersion: "v2", Capabilities: func() []deploymentmodel.FrontendCapabilityDefinition { return nil }})
	if !configured.Configured() || configured.contractVersion != "v2" {
		t.Fatalf("configured service=%#v", configured)
	}

	admin := frontendCapabilityAdmin("workspace-a")
	manifest := validFrontendCapabilityManifest()
	fallbackService := NewDeploymentFrontendCapabilityApplicationService(FrontendCapabilityDependencies{Capabilities: frontendCapabilityDefinitions})
	registered, err := fallbackService.RegisterManifest(t.Context(), manifest, admin)
	if err != nil || registered.Revision != 1 {
		t.Fatalf("fallback registration=%#v err=%v", registered, err)
	}
	registeredAgain, err := fallbackService.RegisterManifest(t.Context(), manifest, admin)
	if err != nil || registeredAgain.Revision != 1 {
		t.Fatalf("fallback second registration=%#v err=%v", registeredAgain, err)
	}
	current, err := fallbackService.Snapshot(t.Context(), admin)
	if err != nil || current.Revision != 1 || current.Manifest == nil {
		t.Fatalf("fallback snapshot=%#v err=%v", current, err)
	}

	repository := newDeploymentFrontendCapabilityMemoryRepository()
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := repository.Get(cancelled, "workspace-a"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled memory get error=%v", err)
	}
	if _, err := repository.Put(cancelled, "workspace-a", []byte(`{}`)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled memory put error=%v", err)
	}
}

func TestFrontendCapabilityRegisterAndSnapshotErrors(t *testing.T) {
	t.Parallel()

	manifest := validFrontendCapabilityManifest()
	admin := frontendCapabilityAdmin("workspace-a")
	nonAdmin := admin
	accessfixture.Set(&nonAdmin, accessfixture.Bundle{})
	service := frontendCapabilityTestService()
	if _, err := service.RegisterManifest(t.Context(), manifest, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("unknown register error=%v", err)
	}
	if _, err := service.RegisterManifest(t.Context(), manifest, nonAdmin); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("non-admin register error=%v", err)
	}
	invalid := manifest
	invalid.Entries = append([]deploymentmodel.FrontendCapabilitySupportEntry(nil), manifest.Entries...)
	invalid.Entries[0].CapabilityKeys = []string{"unknown"}
	if _, err := service.RegisterManifest(t.Context(), invalid, admin); err == nil {
		t.Fatal("invalid manifest registration succeeded")
	}
	if _, err := (*DeploymentFrontendCapabilityApplicationService)(nil).RegisterManifest(t.Context(), manifest, admin); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("nil register error=%v", err)
	}
	if _, err := (*DeploymentFrontendCapabilityApplicationService)(nil).Snapshot(t.Context(), admin); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("nil snapshot error=%v", err)
	}
	if _, err := service.Snapshot(t.Context(), principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("unknown snapshot error=%v", err)
	}

	repository := &frontendCapabilityRepositoryStub{putErr: errFrontendCapabilityRepository}
	service = NewDeploymentFrontendCapabilityApplicationService(FrontendCapabilityDependencies{Repository: repository, Capabilities: frontendCapabilityDefinitions})
	if _, err := service.RegisterManifest(t.Context(), manifest, admin); apperror.CodeOf(err) != "backend.internal" || !errors.Is(err, errFrontendCapabilityRepository) {
		t.Fatalf("register repository error=%v", err)
	}
	repository.putErr, repository.getErr = nil, errFrontendCapabilityRepository
	if _, err := service.Snapshot(t.Context(), admin); apperror.CodeOf(err) != "backend.internal" || !errors.Is(err, errFrontendCapabilityRepository) {
		t.Fatalf("snapshot repository error=%v", err)
	}
	repository.getErr, repository.found = nil, false
	unknown, err := service.Snapshot(t.Context(), admin)
	if err != nil || unknown.Status != "unknown" || unknown.MissingFrontendSupport == nil || unknown.StaleFrontendSupport == nil {
		t.Fatalf("unknown snapshot=%#v err=%v", unknown, err)
	}
	repository.found, repository.state = true, deploymentmodel.DeploymentFrontendCapabilityState{ManifestJSON: []byte(`{`)}
	if _, err := service.Snapshot(t.Context(), admin); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("invalid snapshot error=%v", err)
	}
	repository.state = deploymentmodel.DeploymentFrontendCapabilityState{ManifestJSON: []byte(`{}`), Revision: 1}
	if _, err := service.RegisterManifest(t.Context(), manifest, admin); err != nil || repository.putCalls == 0 {
		t.Fatalf("changed manifest registration err=%v putCalls=%d", err, repository.putCalls)
	}
}

func TestFrontendCapabilityValidationAuthorizationAndBindings(t *testing.T) {
	t.Parallel()

	manifest := validFrontendCapabilityManifest()
	admin := frontendCapabilityAdmin("workspace-a")
	nonAdmin := admin
	accessfixture.Set(&nonAdmin, accessfixture.Bundle{})
	service := frontendCapabilityTestService()
	if _, err := service.ValidateManifest(t.Context(), manifest, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("unknown validation error=%v", err)
	}
	if _, err := service.ValidateManifest(t.Context(), manifest, nonAdmin); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("non-admin validation error=%v", err)
	}

	bindings := FrontendBusinessBindings{
		Objects: map[string]bool{}, Views: map[string]bool{}, Actions: map[string]bool{}, Reports: map[string]bool{}, Fields: map[string]bool{},
	}
	service = frontendCapabilityTestServiceWithBindings(func(context.Context) FrontendBusinessBindings { return bindings })
	manifest.Entries[0].ActorRoles = []string{"missing-role"}
	manifest.Entries[0].BusinessObjects = []string{"missing-object"}
	manifest.Entries[0].ViewKeys = []string{"missing-view"}
	manifest.Entries[0].ImplementedActions = []string{"missing-action"}
	manifest.Entries[0].ReportKeys = []string{"missing-report"}
	manifest.Entries[0].FieldKeys = []string{"missing-field"}
	result, err := service.ValidateManifest(t.Context(), manifest, admin)
	if err != nil || result.Valid || len(result.Issues) < 5 {
		t.Fatalf("binding validation=%#v err=%v", result, err)
	}
	for index := 1; index < len(result.Issues); index++ {
		previous := result.Issues[index-1].FieldPath + "\x00" + result.Issues[index-1].ErrorCode
		current := result.Issues[index].FieldPath + "\x00" + result.Issues[index].ErrorCode
		if previous > current {
			t.Fatalf("issues are not sorted: %#v", result.Issues)
		}
	}
	service.validateBusinessBindings(t.Context(), nil)
	(*DeploymentFrontendCapabilityApplicationService)(nil).validateBusinessBindings(t.Context(), &result)
	NewDeploymentFrontendCapabilityApplicationService().validateBusinessBindings(t.Context(), &result)
	service.enrichSnapshot(t.Context(), nil)
	(*DeploymentFrontendCapabilityApplicationService)(nil).enrichSnapshot(t.Context(), &deploymentmodel.FrontendCapabilitySnapshot{})
}

func TestFrontendCapabilitySnapshotGapClassification(t *testing.T) {
	t.Parallel()

	service := frontendCapabilityTestService()
	manifest := validFrontendCapabilityManifest()
	compatible := service.SnapshotManifest(manifest)
	if compatible.Status != "compatible" || !compatible.ContractCompatible || compatible.RuntimeContractVersion != "runtime-authoring-v1" ||
		compatible.RuntimeCapabilityCount != 1 || compatible.FrontendSupportCount != 1 ||
		compatible.ManifestHash == "" || len(compatible.MissingFrontendSupport) != 0 || len(compatible.StaleFrontendSupport) != 0 {
		t.Fatalf("compatible snapshot=%#v", compatible)
	}
	if normalized, err := service.ValidateManifestDefinition(manifest); err != nil || len(normalized.Entries) != 1 {
		t.Fatalf("valid manifest definition=%#v err=%v", normalized, err)
	}
	manifest.RuntimeContractVersions = []string{"old"}
	manifest.Entries = append(manifest.Entries, deploymentmodel.FrontendCapabilitySupportEntry{SupportKey: "stale"})
	gaps := service.SnapshotManifest(manifest)
	if gaps.Status != "gaps" || gaps.ContractCompatible || len(gaps.StaleFrontendSupport) != 1 {
		t.Fatalf("contract/stale gaps=%#v", gaps)
	}
	manifest.Entries = []deploymentmodel.FrontendCapabilitySupportEntry{{SupportKey: "business", BusinessObjects: []string{"customer"}}}
	gaps = service.SnapshotManifest(manifest)
	if len(gaps.MissingFrontendSupport) != 1 || gaps.MissingFrontendSupport[0].CapabilityKey != "schema.field" || len(gaps.StaleFrontendSupport) != 0 {
		t.Fatalf("missing support gaps=%#v", gaps)
	}
	manifest = validFrontendCapabilityManifest()
	manifest.Entries = append(manifest.Entries, deploymentmodel.FrontendCapabilitySupportEntry{SupportKey: "wrong.support", CapabilityKeys: []string{"schema.field"}})
	gaps = service.SnapshotManifest(manifest)
	if gaps.Status != "gaps" || len(gaps.StaleFrontendSupport) != 1 {
		t.Fatalf("capability support mismatch gaps=%#v", gaps)
	}
	sortingService := NewDeploymentFrontendCapabilityApplicationService(FrontendCapabilityDependencies{Capabilities: func() []deploymentmodel.FrontendCapabilityDefinition {
		return []deploymentmodel.FrontendCapabilityDefinition{{Key: "ignored"}, {Key: "z", FrontendSupportKey: "z.support"}, {Key: "a", FrontendSupportKey: "a.support"}}
	}})
	sorted := sortingService.SnapshotManifest(deploymentmodel.FrontendCapabilityManifest{RuntimeContractVersions: []string{"runtime-authoring-v1"}})
	if len(sorted.MissingFrontendSupport) != 2 || sorted.MissingFrontendSupport[0].CapabilityKey != "a" || sorted.MissingFrontendSupport[1].CapabilityKey != "z" {
		t.Fatalf("missing support is not sorted: %#v", sorted.MissingFrontendSupport)
	}
}

func TestFrontendCapabilityErrorsAndValueHelpers(t *testing.T) {
	t.Parallel()

	warning := deploymentmodel.FrontendCapabilityUsageValidationIssue{Severity: "warning", ErrorCode: "warning"}
	issue := deploymentmodel.FrontendCapabilityUsageValidationIssue{Severity: "error", FieldPath: "entries[0]", ErrorCode: "frontend.invalid", Params: map[string]string{"actual": "bad", "token": "secret"}}
	if err := firstFrontendCapabilityUsageError([]deploymentmodel.FrontendCapabilityUsageValidationIssue{warning}); err != nil {
		t.Fatalf("warning returned error=%v", err)
	}
	err := firstFrontendCapabilityUsageError([]deploymentmodel.FrontendCapabilityUsageValidationIssue{warning, issue})
	if apperror.KindOf(err) != apperror.KindBadRequest || apperror.CodeOf(err) != "frontend.invalid" {
		t.Fatalf("usage error=%v", err)
	}
	if got := valueOrDefault(" value ", "fallback"); got != "value" {
		t.Fatalf("value=%q", got)
	}
	if got := valueOrDefault(" ", " fallback "); got != "fallback" {
		t.Fatalf("fallback=%q", got)
	}
	if err := forbidden("forbidden"); apperror.KindOf(err) != apperror.KindForbidden {
		t.Fatalf("forbidden error=%v", err)
	}
	cause := errors.New("cause")
	if err := internalError("operation", cause); apperror.KindOf(err) != apperror.KindInternal || !errors.Is(err, cause) {
		t.Fatalf("internal error=%v", err)
	}
	appErr := applicationError(apperror.KindConflict, "conflict", cause, " ", "ignored", "field", "value", "dangling")
	if apperror.KindOf(appErr) != apperror.KindConflict || !errors.Is(appErr, cause) {
		t.Fatalf("application error=%v", appErr)
	}
}

func TestFrontendCapabilityMemoryRepositoryCopiesPayload(t *testing.T) {
	t.Parallel()

	repository := newDeploymentFrontendCapabilityMemoryRepository()
	payload := []byte(`{"value":"original"}`)
	state, err := repository.Put(t.Context(), "workspace-a", payload)
	if err != nil {
		t.Fatal(err)
	}
	payload[10] = 'X'
	loaded, found, err := repository.Get(t.Context(), "workspace-a")
	if err != nil || !found || !reflect.DeepEqual(loaded.ManifestJSON, state.ManifestJSON) {
		t.Fatalf("loaded=%#v state=%#v err=%v", loaded, state, err)
	}
}

func validFrontendCapabilityManifest() deploymentmodel.FrontendCapabilityManifest {
	return deploymentmodel.FrontendCapabilityManifest{
		ManifestVersion:         deploymentmodel.FrontendCapabilityManifestVersion,
		FrontendVersion:         "1",
		RuntimeContractVersions: []string{"runtime-authoring-v1"},
		Entries: []deploymentmodel.FrontendCapabilitySupportEntry{{
			SupportKey: "metadata.field.editor.v1", CapabilityKeys: []string{"schema.field"}, Route: "/metadata",
			RequiredPermissions: []string{"workspace.admin"}, FeatureModule: "metadata.tsx", AcceptanceTests: []string{"metadata.spec.ts"},
		}},
	}
}

func frontendCapabilityDefinitions() []deploymentmodel.FrontendCapabilityDefinition {
	return []deploymentmodel.FrontendCapabilityDefinition{{Key: "schema.field", FrontendSupportKey: "metadata.field.editor.v1", Permissions: []string{"workspace.admin"}}}
}
