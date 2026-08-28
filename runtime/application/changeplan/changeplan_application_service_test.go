// Change-plan application service tests.
package changeplan

import (
	identitysdk "github.com/domainry/domainry-identity-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"context"

	auditmodel "github.com/domainry/domainry-runtime/runtime/domain/audit/model"

	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
)

type serviceRepositoryStub struct {
	draft changeplanmodel.BusinessChangePlanDraft
	calls int
}

func (r *serviceRepositoryStub) GetDraft(context.Context, string, string) (changeplanmodel.BusinessChangePlanDraft, bool, error) {
	r.calls++
	return r.draft, r.draft.PlanID != "", nil
}

func (r *serviceRepositoryStub) SaveDraft(_ context.Context, _ string, draft changeplanmodel.BusinessChangePlanDraft, _ int) (changeplanmodel.BusinessChangePlanDraft, bool, error) {
	r.calls++
	r.draft = draft
	return draft, true, nil
}

func (*serviceRepositoryStub) PublishDraft(context.Context, string, string, int, string, string) (changeplanmodel.BusinessChangePlanDraft, bool, error) {
	return changeplanmodel.BusinessChangePlanDraft{}, false, nil
}

func TestChangePlanApplicationAuthorizesWorkspaceBeforeRepositoryAccess(t *testing.T) {
	repository, audit := &serviceRepositoryStub{}, &serviceAuditStub{}
	service := NewChangePlanApplicationService(repository, nil, audit, nil)
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}})
	if _, err := service.Draft(t.Context(), "plan-1", principal); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("draft error=%v", err)
	}
	if _, err := service.SaveDraft(t.Context(), BusinessSystemChangePlan{PlanID: "plan-1"}, 0, principal); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("save error=%v", err)
	}
	if repository.calls != 0 || len(audit.events) != 0 {
		t.Fatalf("repository calls=%d audit events=%d", repository.calls, len(audit.events))
	}
}

type serviceAuditStub struct{ events []auditmodel.AuditEvent }

func (a *serviceAuditStub) InsertAuditEvent(_ context.Context, _ string, event auditmodel.AuditEvent) error {
	a.events = append(a.events, event)
	return nil
}

func TestServiceSavesDraftWithoutCoreAggregate(t *testing.T) {
	repository, audit := &serviceRepositoryStub{}, &serviceAuditStub{}
	service := NewChangePlanApplicationService(repository, nil, audit, nil)
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-1", UserID: "admin"}}, accessfixture.Bundle{Key: "admin", Permissions: []string{"workspace.admin"}})
	draft, err := service.SaveDraft(t.Context(), BusinessSystemChangePlan{PlanID: "plan-1", BusinessReason: "test owner boundary"}, 0, principal)
	if err != nil || draft.Revision != 1 || len(audit.events) != 1 {
		t.Fatalf("draft=%#v audits=%d err=%v", draft, len(audit.events), err)
	}
}

func TestServiceRequiresExactConfirmationAndRejectsUnsupportedLifecycle(t *testing.T) {
	audit := &serviceAuditStub{}
	service := NewChangePlanApplicationService(&serviceRepositoryStub{}, nil, audit, nil)
	snapshot := changePlanTestSnapshot()
	graph := ReferenceGraph{Version: BusinessReferenceGraphVersion, Hash: "graph-hash"}
	plan := changePlanTestPlan(snapshot, graph)

	if _, err := service.Apply(t.Context(), plan, "wrong", snapshot, graph, changePlanAdmin()); ErrorCodeOf(err) != "backend.change_plan.confirmation_invalid" {
		t.Fatalf("confirmation error=%v", err)
	}
	plan.Items[0] = BusinessSystemChangeItem{ItemID: "unsupported", Operation: "create", ChangeKind: "additive", RiskLevel: "low", ResourceType: "unknown_resource", ResourceKey: "approval", ResourceOwner: "builder", CapabilityKey: "unknown", After: []byte(`{"key":"approval"}`), ValidationMethods: []string{"metadata.validate"}}
	plan.ReleaseOrder, plan.RollbackOrder = []string{"unsupported"}, []string{"unsupported"}
	if _, err := service.Apply(t.Context(), plan, plan.PlanID, snapshot, graph, changePlanAdmin()); ErrorCodeOf(err) != "backend.change_plan.apply_not_allowed" {
		t.Fatalf("lifecycle error=%v", err)
	}
	if len(audit.events) != 2 || audit.events[0].Event != "business_change_plan.failed" || audit.events[1].Event != "business_change_plan.failed" {
		t.Fatalf("audit events=%#v", audit.events)
	}
}
