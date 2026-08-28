package lifecycle

import (
	"context"
	"errors"
	"testing"
	"time"

	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
	lifecyclepolicy "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/policy"
	"github.com/domainry/domainry-runtime/runtime/platform/requestcontext"
)

func lifecycleValidPolicyVersion(now time.Time) lifecyclemodel.PolicyVersion {
	return lifecyclemodel.PolicyVersion{
		Policy: lifecyclemodel.RetentionPolicy{
			Key:                "generic.records.v1",
			Version:            "1",
			Owner:              "integration",
			Class:              lifecyclemodel.RetentionClassTechnical,
			DefaultRetention:   time.Hour,
			MinimumRetention:   15 * time.Minute,
			WorkspaceMayExtend: true,
			BackupBehavior:     lifecyclemodel.BackupBehaviorStandard,
			EraseBehavior:      lifecyclemodel.EraseBehaviorDelete,
		},
		Status:      lifecyclemodel.PolicyStatusPublished,
		Revision:    1,
		PublishedAt: now,
	}
}

func TestLifecycleInstallDefaultPoliciesCoversDependencyAndPersistenceFailures(t *testing.T) {
	now := time.Date(2026, 7, 19, 6, 7, 8, 0, time.UTC)
	admin := lifecycleAdmin("workspace-a", "admin")
	var nilService *LifecycleApplicationService
	if err := nilService.InstallDefaultPolicies(t.Context(), "workspace-a", admin, now); err == nil {
		t.Fatal("nil service installed policies")
	}
	service := NewLifecycleApplicationService(t.Context(), LifecycleApplicationDependencies{})
	if err := service.InstallDefaultPolicies(t.Context(), "workspace-a", admin, now); err == nil {
		t.Fatal("missing repository accepted")
	}
	repository := &lifecycleRepositoryProbe{}
	service = NewLifecycleApplicationService(t.Context(), LifecycleApplicationDependencies{Repository: repository})
	if err := service.InstallDefaultPolicies(t.Context(), "workspace-b", admin, now); err == nil {
		t.Fatal("cross-workspace policy installation accepted")
	}
	lookupFailure := errors.New("policy lookup failed")
	repository.latestPolicy = func(context.Context, string, string) (lifecyclemodel.PolicyVersion, bool, error) {
		return lifecyclemodel.PolicyVersion{}, false, lookupFailure
	}
	if err := service.InstallDefaultPolicies(t.Context(), "workspace-a", admin, now); !errors.Is(err, lookupFailure) {
		t.Fatalf("lookup error = %v", err)
	}
	repository.latestPolicy = func(context.Context, string, string) (lifecyclemodel.PolicyVersion, bool, error) {
		return lifecyclemodel.PolicyVersion{}, false, nil
	}
	saveFailure := errors.New("policy save failed")
	repository.savePolicy = func(context.Context, lifecyclemodel.PolicyVersion) error { return saveFailure }
	if err := service.InstallDefaultPolicies(t.Context(), "workspace-a", admin, now); !errors.Is(err, saveFailure) {
		t.Fatalf("save error = %v", err)
	}

	saved := 0
	repository.savePolicy = func(_ context.Context, version lifecyclemodel.PolicyVersion) error {
		if version.WorkspaceID != "workspace-a" || version.PublishedBy != admin.UserID || version.PublishedAt != now {
			t.Fatalf("version=%#v", version)
		}
		saved++
		return nil
	}
	auditFailure := errors.New("install audit failed")
	repository.appendAudit = func(context.Context, lifecyclemodel.AuditEvidence) error { return auditFailure }
	if err := service.InstallDefaultPolicies(t.Context(), "workspace-a", admin, now); !errors.Is(err, auditFailure) {
		t.Fatalf("audit error = %v", err)
	}
	if saved != len(lifecyclepolicy.DefaultPolicyCatalog("workspace-a", admin.UserID, now)) {
		t.Fatalf("saved=%d", saved)
	}

	saved = 0
	repository.appendAudit = nil
	repository.latestPolicy = func(context.Context, string, string) (lifecyclemodel.PolicyVersion, bool, error) {
		return lifecyclemodel.PolicyVersion{}, true, nil
	}
	if err := service.InstallDefaultPolicies(t.Context(), "workspace-a", admin, now); err != nil || saved != 0 {
		t.Fatalf("saved=%d err=%v", saved, err)
	}
}

func TestLifecycleInstallDefaultPoliciesBindsAuthorizedWorkspaceContext(t *testing.T) {
	workspaceID := "workspace-a"
	contextChecks := 0
	repository := &lifecycleRepositoryProbe{}
	repository.latestPolicy = func(ctx context.Context, gotWorkspaceID, _ string) (lifecyclemodel.PolicyVersion, bool, error) {
		if gotWorkspaceID != workspaceID || requestcontext.WorkspaceID(ctx) != workspaceID {
			t.Fatalf("lookup workspace argument=%q context=%q", gotWorkspaceID, requestcontext.WorkspaceID(ctx))
		}
		contextChecks++
		return lifecyclemodel.PolicyVersion{}, true, nil
	}
	repository.appendAudit = func(ctx context.Context, evidence lifecyclemodel.AuditEvidence) error {
		if evidence.WorkspaceID != workspaceID || requestcontext.WorkspaceID(ctx) != workspaceID {
			t.Fatalf("audit workspace evidence=%q context=%q", evidence.WorkspaceID, requestcontext.WorkspaceID(ctx))
		}
		contextChecks++
		return nil
	}
	service := NewLifecycleApplicationService(t.Context(), LifecycleApplicationDependencies{Repository: repository})
	if err := service.InstallDefaultPolicies(t.Context(), workspaceID, lifecycleAdmin(workspaceID, "admin"), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if contextChecks != len(lifecyclepolicy.DefaultPolicyCatalog(workspaceID, "admin", time.Time{}))+1 {
		t.Fatalf("workspace context checks=%d", contextChecks)
	}
}

func TestLifecyclePublishPolicyValidatesVersionsAndPreservesFailures(t *testing.T) {
	now := time.Date(2026, 7, 19, 7, 8, 9, 0, time.UTC)
	admin := lifecycleAdmin("workspace-a", "publisher")
	version := lifecycleValidPolicyVersion(now)
	var nilService *LifecycleApplicationService
	if _, err := nilService.PublishPolicy(t.Context(), version, admin); err == nil {
		t.Fatal("nil service published policy")
	}
	repository := &lifecycleRepositoryProbe{}
	service := NewLifecycleApplicationService(t.Context(), LifecycleApplicationDependencies{Repository: repository})
	lookupFailure := errors.New("latest policy failed")
	repository.latestPolicy = func(context.Context, string, string) (lifecyclemodel.PolicyVersion, bool, error) {
		return lifecyclemodel.PolicyVersion{}, false, lookupFailure
	}
	if _, err := service.PublishPolicy(t.Context(), version, admin); !errors.Is(err, lookupFailure) {
		t.Fatalf("lookup error = %v", err)
	}
	repository.latestPolicy = func(context.Context, string, string) (lifecyclemodel.PolicyVersion, bool, error) {
		return lifecyclemodel.PolicyVersion{}, false, nil
	}
	invalid := version
	invalid.Status = lifecyclemodel.PolicyStatusDraft
	if _, err := service.PublishPolicy(t.Context(), invalid, admin); err == nil {
		t.Fatal("draft policy published")
	}
	previous := version
	previous.WorkspaceID, previous.PublishedBy = admin.WorkspaceID, admin.UserID
	repository.latestPolicy = func(context.Context, string, string) (lifecyclemodel.PolicyVersion, bool, error) {
		return previous, true, nil
	}
	if _, err := service.PublishPolicy(t.Context(), version, admin); err == nil {
		t.Fatal("policy revision did not increase")
	}
	next := version
	next.Revision = 2
	saveFailure := errors.New("policy save failed")
	repository.savePolicy = func(context.Context, lifecyclemodel.PolicyVersion) error { return saveFailure }
	if _, err := service.PublishPolicy(t.Context(), next, admin); !errors.Is(err, saveFailure) {
		t.Fatalf("save error = %v", err)
	}
	repository.savePolicy = nil
	auditFailure := errors.New("publish audit failed")
	repository.appendAudit = func(context.Context, lifecyclemodel.AuditEvidence) error { return auditFailure }
	if _, err := service.PublishPolicy(t.Context(), next, admin); !errors.Is(err, auditFailure) {
		t.Fatalf("audit error = %v", err)
	}
	repository.appendAudit = nil
	published, err := service.PublishPolicy(t.Context(), next, admin)
	if err != nil || published.WorkspaceID != admin.WorkspaceID || published.PublishedBy != admin.UserID || published.Revision != 2 {
		t.Fatalf("published=%#v err=%v", published, err)
	}
}

func TestLifecycleLegalHoldCreationAndReleaseFailureBoundaries(t *testing.T) {
	now := time.Date(2026, 7, 19, 8, 9, 10, 0, time.UTC)
	admin := lifecycleAdmin("workspace-a", "legal-admin")
	hold := lifecyclemodel.LegalHold{ID: "hold-1", WorkspaceID: "workspace-a", Owner: "record", ResourceType: "record", ResourceID: "record-1", Reason: "legal case", Authority: "legal", StartsAt: now, ReviewAt: now.Add(time.Hour), AuditEvidence: "case-1"}
	repository := &lifecycleRepositoryProbe{}
	service := NewLifecycleApplicationService(t.Context(), LifecycleApplicationDependencies{Repository: repository})
	wrongWorkspace := hold
	wrongWorkspace.WorkspaceID = "workspace-b"
	if _, err := service.CreateLegalHold(t.Context(), wrongWorkspace, admin); err == nil {
		t.Fatal("cross-workspace legal hold accepted")
	}
	invalid := hold
	invalid.Reason = ""
	if _, err := service.CreateLegalHold(t.Context(), invalid, admin); err == nil {
		t.Fatal("invalid legal hold accepted")
	}
	saveFailure := errors.New("legal hold save failed")
	repository.saveLegalHold = func(context.Context, lifecyclemodel.LegalHold) error { return saveFailure }
	if _, err := service.CreateLegalHold(t.Context(), hold, admin); !errors.Is(err, saveFailure) {
		t.Fatalf("create save error = %v", err)
	}
	repository.saveLegalHold = nil
	auditFailure := errors.New("legal hold audit failed")
	repository.appendAudit = func(context.Context, lifecyclemodel.AuditEvidence) error { return auditFailure }
	if _, err := service.CreateLegalHold(t.Context(), hold, admin); !errors.Is(err, auditFailure) {
		t.Fatalf("create audit error = %v", err)
	}
	repository.appendAudit = nil
	created, err := service.CreateLegalHold(t.Context(), hold, admin)
	if err != nil || created.ID != hold.ID {
		t.Fatalf("created=%#v err=%v", created, err)
	}

	getFailure := errors.New("legal hold lookup failed")
	repository.getLegalHold = func(context.Context, string, string) (lifecyclemodel.LegalHold, bool, error) {
		return lifecyclemodel.LegalHold{}, false, getFailure
	}
	if _, err := service.EndLegalHold(t.Context(), "workspace-a", hold.ID, "legal", "closed", now.Add(time.Hour), admin); err == nil {
		t.Fatal("lookup failure did not stop release")
	}
	repository.getLegalHold = func(context.Context, string, string) (lifecyclemodel.LegalHold, bool, error) { return hold, true, nil }
	if _, err := service.EndLegalHold(t.Context(), "workspace-a", hold.ID, "", "closed", now.Add(time.Hour), admin); err == nil {
		t.Fatal("release without authority accepted")
	}
	repository.saveLegalHold = func(context.Context, lifecyclemodel.LegalHold) error { return saveFailure }
	if _, err := service.EndLegalHold(t.Context(), "workspace-a", hold.ID, "legal", "closed", now.Add(time.Hour), admin); !errors.Is(err, saveFailure) {
		t.Fatalf("release save error = %v", err)
	}
	repository.saveLegalHold = nil
	repository.appendAudit = func(context.Context, lifecyclemodel.AuditEvidence) error { return auditFailure }
	if _, err := service.EndLegalHold(t.Context(), "workspace-a", hold.ID, "legal", "closed", now.Add(time.Hour), admin); !errors.Is(err, auditFailure) {
		t.Fatalf("release audit error = %v", err)
	}
	repository.appendAudit = nil
	ended, err := service.EndLegalHold(t.Context(), "workspace-a", hold.ID, " legal ", " closed ", now.Add(time.Hour), admin)
	if err != nil || ended.EndsAt == nil || ended.Authority != "legal" || ended.AuditEvidence != "closed" {
		t.Fatalf("ended=%#v err=%v", ended, err)
	}
}

func TestLifecyclePolicyExecutorRequiresPublishedPolicyOwner(t *testing.T) {
	repositoryFailure := errors.New("policy lookup failed")
	repository := &lifecycleRepositoryProbe{latestPolicy: func(context.Context, string, string) (lifecyclemodel.PolicyVersion, bool, error) {
		return lifecyclemodel.PolicyVersion{}, false, repositoryFailure
	}}
	service := NewLifecycleApplicationService(t.Context(), LifecycleApplicationDependencies{Repository: repository})
	if _, _, err := service.policyExecutor(t.Context(), "workspace-a", "policy-1"); err == nil {
		t.Fatal("repository failure accepted as policy")
	}
	version := lifecycleValidPolicyVersion(time.Now().UTC())
	repository.latestPolicy = func(context.Context, string, string) (lifecyclemodel.PolicyVersion, bool, error) {
		return version, true, nil
	}
	if _, _, err := service.policyExecutor(t.Context(), "workspace-a", version.Policy.Key); err == nil {
		t.Fatal("policy without owner executor accepted")
	}
	executor := &recoverableCleanupExecutor{}
	service.executors[version.Policy.Owner] = executor
	gotVersion, gotExecutor, err := service.policyExecutor(t.Context(), "workspace-a", version.Policy.Key)
	if err != nil || gotVersion.Policy.Key != version.Policy.Key || gotExecutor != executor {
		t.Fatalf("version=%#v executor=%T err=%v", gotVersion, gotExecutor, err)
	}
}
