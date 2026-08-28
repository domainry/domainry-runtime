package scheduler

import (
	"context"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type schedulerWorkspaceAuthorizationProbe struct{ calls int }

func (p *schedulerWorkspaceAuthorizationProbe) GetRecord(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
	p.calls++
	return recordmodel.Record{}, false, nil
}

func (p *schedulerWorkspaceAuthorizationProbe) ListRecords(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	p.calls++
	return recordmodel.RecordPageResult{}, nil
}

func (p *schedulerWorkspaceAuthorizationProbe) UpdateRecordWhere(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, map[string]any) (bool, error) {
	p.calls++
	return false, nil
}

func (p *schedulerWorkspaceAuthorizationProbe) CommitRecordMutationBatch(context.Context, string, []transactionmodel.RecordMutationCommit) error {
	p.calls++
	return nil
}

func TestSchedulerApplicationAuthorizesWorkspaceBeforeRepositoryAccess(t *testing.T) {
	probe := &schedulerWorkspaceAuthorizationProbe{}
	service := NewSchedulerApplicationService(nil, nil, probe, nil)
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}})

	checks := []func() error{
		func() error {
			_, err := service.RunJob(t.Context(), "definition-1", "request-1", principal)
			return err
		},
		func() error { _, err := service.ProcessDueJobs(t.Context(), 1, principal, "scheduler"); return err },
		func() error {
			_, _, err := service.ClaimRun(t.Context(), recordmodel.Record{ID: "definition-1"}, "scheduler", time.Now(), principalmodel.SystemScope{})
			return err
		},
		func() error { _, err := service.Status(t.Context(), principalmodel.SystemScope{}); return err },
	}
	for index, check := range checks {
		if code := apperror.CodeOf(check()); code != "backend.workspace_scope_required" && code != "backend.system_scope_required" {
			t.Fatalf("check %d code=%q", index, code)
		}
	}
	if probe.calls != 0 {
		t.Fatalf("repository called before scheduler scope authorization: %d", probe.calls)
	}
}
