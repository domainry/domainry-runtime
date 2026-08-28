package lifecycle

import (
	"context"
	"errors"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	lifecyclecontract "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/contract"
	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type lifecycleNamedExecutor struct{ owner string }

func (e lifecycleNamedExecutor) Owner(context.Context) string { return e.owner }
func (lifecycleNamedExecutor) Preview(context.Context, string, lifecyclemodel.PolicyVersion, time.Time) (lifecyclecontract.CleanupPreview, error) {
	return lifecyclecontract.CleanupPreview{}, nil
}
func (lifecycleNamedExecutor) ProcessBatch(context.Context, lifecyclemodel.CleanupJob, lifecyclemodel.PolicyVersion, []lifecyclemodel.LegalHold, int) (lifecyclemodel.CleanupBatchResult, error) {
	return lifecyclemodel.CleanupBatchResult{Done: true}, nil
}

func TestLifecycleFinalAuthorizationConditions(t *testing.T) {
	service := NewLifecycleApplicationService(t.Context(), LifecycleApplicationDependencies{Repository: &lifecycleRepositoryProbe{}})
	unknown := principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace-a"}}
	now := time.Now().UTC()
	checks := []func() error{
		func() error {
			_, err := service.PublishPolicy(t.Context(), lifecyclemodel.PolicyVersion{}, unknown)
			return err
		},
		func() error {
			_, err := service.CreateLegalHold(t.Context(), lifecyclemodel.LegalHold{}, unknown)
			return err
		},
		func() error {
			_, err := service.PreviewCleanup(t.Context(), "workspace-a", "policy", unknown, now)
			return err
		},
		func() error {
			_, err := service.CreateCleanupJob(t.Context(), lifecyclemodel.CleanupJob{WorkspaceID: "workspace-a"}, unknown)
			return err
		},
		func() error {
			_, err := service.ProcessCleanupJob(t.Context(), "workspace-a", "job", "worker", time.Minute, 1, now, unknown)
			return err
		},
		func() error {
			_, err := service.CreateSubjectRequest(t.Context(), lifecyclemodel.SubjectRequest{WorkspaceID: "workspace-a"}, unknown)
			return err
		},
		func() error {
			_, err := service.VerifySubjectRequest(t.Context(), "workspace-a", "request", "proof", unknown)
			return err
		},
		func() error {
			_, err := service.PreviewSubjectRequest(t.Context(), "workspace-a", "request", unknown)
			return err
		},
		func() error {
			_, err := service.ApproveSubjectRequest(t.Context(), "workspace-a", "request", unknown)
			return err
		},
		func() error {
			_, err := service.ExecuteSubjectRequest(t.Context(), "workspace-a", "request", unknown)
			return err
		},
		func() error {
			_, err := service.DownloadSubjectExport(t.Context(), "workspace-a", "request", unknown, now)
			return err
		},
		func() error { return service.InstallDefaultPolicies(t.Context(), "workspace-a", unknown, now) },
	}
	for index, check := range checks {
		if err := check(); err == nil {
			t.Fatalf("authorization check %d succeeded", index)
		}
	}
	invalidWorkspace := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: " "}}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}})
	if err := lifecycleAuthorize(invalidWorkspace, PermissionPolicyManage); err == nil {
		t.Fatal("invalid workspace authorized")
	}
}

func TestLifecycleFinalConstructorRepositoryAndLegalHoldConditions(t *testing.T) {
	service := NewLifecycleApplicationService(t.Context(), LifecycleApplicationDependencies{Executors: []lifecyclecontract.OwnerLifecycleExecutor{nil, lifecycleNamedExecutor{}, lifecycleNamedExecutor{owner: "valid"}}})
	if len(service.executors) != 1 || service.executors["valid"] == nil {
		t.Fatalf("executors=%#v", service.executors)
	}
	admin := lifecycleAdmin("workspace-a", "admin")
	if _, err := service.PublishPolicy(t.Context(), lifecyclemodel.PolicyVersion{}, admin); err == nil {
		t.Fatal("nil repository published policy")
	}

	now := time.Now().UTC()
	hold := lifecyclemodel.LegalHold{ID: "hold", WorkspaceID: "workspace-a", StartsAt: now}
	repository := &lifecycleRepositoryProbe{}
	service = NewLifecycleApplicationService(t.Context(), LifecycleApplicationDependencies{Repository: repository})
	if _, err := service.EndLegalHold(t.Context(), "workspace-a", hold.ID, "legal", "proof", now.Add(time.Hour), admin); err == nil {
		t.Fatal("missing legal hold ended")
	}
	repository.getLegalHold = func(context.Context, string, string) (lifecyclemodel.LegalHold, bool, error) { return hold, true, nil }
	for _, input := range []struct {
		evidence string
		endedAt  time.Time
	}{
		{"", now.Add(time.Hour)},
		{"proof", time.Time{}},
		{"proof", now},
	} {
		if _, err := service.EndLegalHold(t.Context(), "workspace-a", hold.ID, "legal", input.evidence, input.endedAt, admin); err == nil {
			t.Fatalf("invalid legal hold release=%#v", input)
		}
	}
}

func TestLifecycleFinalPolicyCleanupAndSubjectLookupConditions(t *testing.T) {
	now := time.Now().UTC()
	admin := lifecycleAdmin("workspace-a", "admin")
	repository := &lifecycleRepositoryProbe{}
	service := NewLifecycleApplicationService(t.Context(), LifecycleApplicationDependencies{Repository: repository})
	if _, err := service.PreviewCleanup(t.Context(), "workspace-a", "missing", admin, now); err == nil {
		t.Fatal("missing preview policy accepted")
	}
	if _, err := service.CreateCleanupJob(t.Context(), lifecyclemodel.CleanupJob{WorkspaceID: "workspace-a", PolicyKey: "missing"}, admin); err == nil {
		t.Fatal("missing cleanup policy accepted")
	}

	lookupFailure := errors.New("subject lookup failed")
	repository.getSubjectRequest = func(context.Context, string, string) (lifecyclemodel.SubjectRequest, bool, error) {
		return lifecyclemodel.SubjectRequest{}, false, lookupFailure
	}
	for index, lookup := range []func() error{
		func() error {
			_, err := service.VerifySubjectRequest(t.Context(), "workspace-a", "request", "proof", admin)
			return err
		},
		func() error {
			_, err := service.PreviewSubjectRequest(t.Context(), "workspace-a", "request", admin)
			return err
		},
		func() error {
			_, err := service.ApproveSubjectRequest(t.Context(), "workspace-a", "request", admin)
			return err
		},
		func() error {
			_, err := service.ExecuteSubjectRequest(t.Context(), "workspace-a", "request", admin)
			return err
		},
	} {
		if err := lookup(); !errors.Is(err, lookupFailure) {
			t.Fatalf("subject lookup %d error=%v", index, err)
		}
	}
}

func TestLifecycleFinalExplicitCleanupStatusAndSubjectFields(t *testing.T) {
	now := time.Now().UTC()
	service, _, executor, version := newLifecycleCleanupProbe(t, now)
	executor.preview = func(context.Context, string, lifecyclemodel.PolicyVersion, time.Time) (lifecyclecontract.CleanupPreview, error) {
		return lifecyclecontract.CleanupPreview{}, nil
	}
	admin := lifecycleAdmin("workspace-a", "admin")
	job := lifecyclemodel.CleanupJob{ID: "job", WorkspaceID: "workspace-a", PolicyKey: version.Policy.Key, Operation: lifecyclemodel.OperationPurge, Reason: "retention", Status: lifecyclemodel.CleanupStatusPending, CreatedAt: now}
	if saved, err := service.CreateCleanupJob(t.Context(), job, admin); err != nil || saved.Status != lifecyclemodel.CleanupStatusPending {
		t.Fatalf("explicit cleanup status=%#v err=%v", saved, err)
	}

	base := lifecyclemodel.SubjectRequest{ID: "request", WorkspaceID: "workspace-a", Kind: lifecyclemodel.SubjectRequestExport, SubjectID: "subject", SubjectType: "user", Reason: "reason"}
	for _, mutate := range []func(*lifecyclemodel.SubjectRequest){
		func(value *lifecyclemodel.SubjectRequest) { value.SubjectType = "" },
		func(value *lifecyclemodel.SubjectRequest) { value.Reason = "" },
	} {
		request := base
		mutate(&request)
		if _, err := service.CreateSubjectRequest(t.Context(), request, admin); err == nil {
			t.Fatalf("invalid subject request accepted=%#v", request)
		}
	}
}

func TestLifecycleFinalRunnableCleanupSuccessCondition(t *testing.T) {
	now := time.Now().UTC()
	version := lifecycleValidPolicyVersion(now)
	job := lifecyclemodel.CleanupJob{ID: "job", WorkspaceID: "workspace-a", PolicyKey: version.Policy.Key}
	repository := &lifecycleRepositoryProbe{
		latestPolicy: func(context.Context, string, string) (lifecyclemodel.PolicyVersion, bool, error) {
			return version, true, nil
		},
		listRunnableJobs: func(context.Context, principalmodel.SystemScope, int, time.Time) ([]lifecyclemodel.CleanupJob, error) {
			return []lifecyclemodel.CleanupJob{job}, nil
		},
		claimCleanupJob: func(context.Context, string, string, string, time.Duration, time.Time) (lifecyclemodel.CleanupJob, bool, error) {
			return job, true, nil
		},
	}
	service := NewLifecycleApplicationService(t.Context(), LifecycleApplicationDependencies{Repository: repository, Executors: []lifecyclecontract.OwnerLifecycleExecutor{lifecycleNamedExecutor{owner: version.Policy.Owner}}})
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "cleanup test")
	if processed, err := service.ProcessRunnableCleanupJobs(t.Context(), "worker", 10, 10, now, scope); err != nil || processed != 1 {
		t.Fatalf("processed=%d err=%v", processed, err)
	}
}
