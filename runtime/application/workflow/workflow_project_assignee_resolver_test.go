package workflow

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type workflowProjectAssigneeResolverStub struct {
	descriptor runtimeext.AssigneeResolverDescriptor
	resolve    func(context.Context, runtimeext.AssigneeResolverCapabilities, runtimeext.AssigneeResolverContext) ([]runtimeext.AssigneeResolverCandidate, error)
}

func (s workflowProjectAssigneeResolverStub) Descriptor() runtimeext.AssigneeResolverDescriptor {
	return s.descriptor
}

func (s workflowProjectAssigneeResolverStub) Resolve(ctx context.Context, capabilities runtimeext.AssigneeResolverCapabilities, request runtimeext.AssigneeResolverContext) ([]runtimeext.AssigneeResolverCandidate, error) {
	return s.resolve(ctx, capabilities, request)
}

func workflowProjectResolverDescriptor() runtimeext.AssigneeResolverDescriptor {
	descriptor := runtimeext.AssigneeResolverDescriptor{
		ResolverKey: "finance.approver", ResolverRevision: "revision-1",
		ConfigFields:        []runtimeext.AssigneeResolverConfigField{{Key: "threshold", Type: runtimeext.AssigneeResolverConfigInteger, Required: true}},
		RecordCapabilities:  []runtimeext.AssigneeResolverRecordCapability{{Key: "request", ObjectKey: "order", Fields: []string{"amount"}, FilterFields: []string{"region"}, MaxRows: 2}},
		IdentityProjections: []string{runtimeext.AssigneeIdentityProjectionFindUser}, CandidateRoleKeys: []string{"finance"},
		MaxReadOperations: 2, MaxCandidates: 2, TimeoutMilliseconds: 100,
	}
	descriptor.ConfigContractSHA256 = descriptor.ComputedConfigContractSHA256()
	return descriptor
}

func workflowProjectResolverEngine(t *testing.T, resolver workflowProjectAssigneeResolverStub) *WorkflowProcessEngine {
	t.Helper()
	registry := runtimeext.NewProjectExtensionRegistry()
	if err := registry.RegisterAssigneeResolver(resolver); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	identity := workflowApprovalIdentityStub{users: map[string]identitysdk.User{
		"approver": {ID: "approver", Status: identitysdk.UserStatusActive},
	}, roles: []identitysdk.Role{{ID: "finance-role", Key: "finance"}}, assignments: map[string][]identitysdk.UserRoleAssignment{
		"approver": {{UserID: "approver", RoleID: "finance-role"}},
	}}
	return NewWorkflowProcessEngine(WorkflowDependencies{
		Identity: identity, ProjectExtensions: registry,
		RecordReader: workflowRecordReaderEdgeStub{records: map[string]recordmodel.Record{
			"record": {ID: "record", Data: map[string]any{"amount": "1200", "secret": "must-not-project"}},
		}},
		ObjectMap: func(context.Context) map[string]definitionmodel.ObjectSchema {
			return map[string]definitionmodel.ObjectSchema{"order": {Key: "order", Fields: []definitionmodel.FieldSchema{{Key: "amount", Type: "number"}, {Key: "region", Type: "text"}, {Key: "secret", Type: "text"}}}}
		},
	})
}

func TestProjectAssigneeResolverRejectsUnassignedCandidateRole(t *testing.T) {
	descriptor := workflowProjectResolverDescriptor()
	resolver := workflowProjectAssigneeResolverStub{descriptor: descriptor, resolve: func(context.Context, runtimeext.AssigneeResolverCapabilities, runtimeext.AssigneeResolverContext) ([]runtimeext.AssigneeResolverCandidate, error) {
		return []runtimeext.AssigneeResolverCandidate{{UserID: "approver", RoleKey: "finance"}}, nil
	}}
	engine := workflowProjectResolverEngine(t, resolver)
	identity := engine.runtime.dependencies.Identity.(workflowApprovalIdentityStub)
	identity.assignments = nil
	engine.runtime.dependencies.Identity = identity
	process := workflowmodel.WorkflowProcessInstance{WorkspaceID: "workspace", ID: "process", WorkflowKey: "expense", DefinitionVersion: 3, ObjectKey: "order", RecordID: "record"}
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace", UserID: "requester"}}
	if _, err := engine.resolveProjectAssignees(t.Context(), process, "approval", definitionmodel.WorkflowAssigneeResolver{Type: "project", ResolverKey: descriptor.ResolverKey, Config: map[string]any{"threshold": float64(1000)}}, 0, principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("unassigned role error=%v", err)
	}
}

func TestProjectAssigneeResolverUsesOnlyDeclaredCapabilitiesAndPersistsEvidence(t *testing.T) {
	descriptor := workflowProjectResolverDescriptor()
	resolver := workflowProjectAssigneeResolverStub{descriptor: descriptor, resolve: func(ctx context.Context, capabilities runtimeext.AssigneeResolverCapabilities, request runtimeext.AssigneeResolverContext) ([]runtimeext.AssigneeResolverCandidate, error) {
		if request.Config["threshold"] != float64(1000) || request.NodeID != "approval" {
			t.Fatalf("request=%#v", request)
		}
		record, found, err := capabilities.GetRecord(ctx, "request", request.RecordID)
		if err != nil || !found || record.Fields["amount"] != "1200" {
			t.Fatalf("record=%#v found=%v err=%v", record, found, err)
		}
		if _, leaked := record.Fields["secret"]; leaked {
			t.Fatalf("undeclared field leaked: %#v", record.Fields)
		}
		if _, _, err := capabilities.GetRecord(ctx, "undeclared", request.RecordID); !errors.Is(err, runtimeext.ErrAssigneeResolverGrantDenied) {
			t.Fatalf("undeclared capability error=%v", err)
		}
		return []runtimeext.AssigneeResolverCandidate{{UserID: "approver", RoleKey: "finance", Evidence: []runtimeext.AssigneeEvidenceFact{{Key: "amount_band", Value: "high"}}}}, nil
	}}
	engine := workflowProjectResolverEngine(t, resolver)
	process := workflowmodel.WorkflowProcessInstance{WorkspaceID: "workspace", ID: "process", WorkflowKey: "expense", DefinitionVersion: 3, ObjectKey: "order", RecordID: "record"}
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace", UserID: "requester"}}
	resolved, err := engine.resolveProjectAssignees(t.Context(), process, "approval", definitionmodel.WorkflowAssigneeResolver{Type: "project", ResolverKey: "finance.approver", Config: map[string]any{"threshold": 1000}}, 0, principal)
	if err != nil || len(resolved) != 1 || resolved[0].UserID != "approver" || resolved[0].RoleKey != "finance" || resolved[0].ResolverKey != "finance.approver" {
		t.Fatalf("resolved=%+v err=%v", resolved, err)
	}
	match := resolved[0].Evidence.Matches[0]
	if match.ResolverType != "project" || match.ResolverKey != "finance.approver" || len(match.Facts) != 1 || match.Facts[0].Value != "high" {
		t.Fatalf("evidence=%+v", match)
	}
}

func TestProjectAssigneeResolverFailsClosedOnRegistrationConfigBudgetAndResult(t *testing.T) {
	descriptor := workflowProjectResolverDescriptor()
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace", UserID: "requester"}}
	process := workflowmodel.WorkflowProcessInstance{WorkspaceID: "workspace", ObjectKey: "order", RecordID: "record"}
	missing := NewWorkflowProcessEngine(WorkflowDependencies{Identity: workflowApprovalIdentityStub{}})
	if _, err := missing.resolveProjectAssignees(t.Context(), process, "node", definitionmodel.WorkflowAssigneeResolver{ResolverKey: "missing"}, 0, principal); apperror.CodeOf(err) != "backend.workflow.resolver_not_registered" {
		t.Fatalf("missing resolver error=%v", err)
	}

	valid := workflowProjectAssigneeResolverStub{descriptor: descriptor, resolve: func(context.Context, runtimeext.AssigneeResolverCapabilities, runtimeext.AssigneeResolverContext) ([]runtimeext.AssigneeResolverCandidate, error) {
		return []runtimeext.AssigneeResolverCandidate{{UserID: "approver"}}, nil
	}}
	engine := workflowProjectResolverEngine(t, valid)
	if _, err := engine.resolveProjectAssignees(t.Context(), process, "node", definitionmodel.WorkflowAssigneeResolver{ResolverKey: descriptor.ResolverKey}, 0, principal); apperror.CodeOf(err) != "backend.workflow.resolver_config_invalid" {
		t.Fatalf("config error=%v", err)
	}

	descriptor.MaxReadOperations = 1
	budget := workflowProjectAssigneeResolverStub{descriptor: descriptor, resolve: func(ctx context.Context, capabilities runtimeext.AssigneeResolverCapabilities, _ runtimeext.AssigneeResolverContext) ([]runtimeext.AssigneeResolverCandidate, error) {
		if _, _, err := capabilities.FindUser(ctx, "approver"); err != nil {
			return nil, err
		}
		_, _, err := capabilities.FindUser(ctx, "approver")
		return nil, err
	}}
	if _, err := workflowProjectResolverEngine(t, budget).resolveProjectAssignees(t.Context(), process, "node", definitionmodel.WorkflowAssigneeResolver{ResolverKey: descriptor.ResolverKey, Config: map[string]any{"threshold": 1}}, 0, principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("budget error=%v", err)
	}

	descriptor.MaxCandidates = 1
	tooMany := workflowProjectAssigneeResolverStub{descriptor: descriptor, resolve: func(context.Context, runtimeext.AssigneeResolverCapabilities, runtimeext.AssigneeResolverContext) ([]runtimeext.AssigneeResolverCandidate, error) {
		return []runtimeext.AssigneeResolverCandidate{{UserID: "approver"}, {UserID: "other"}}, nil
	}}
	if _, err := workflowProjectResolverEngine(t, tooMany).resolveProjectAssignees(t.Context(), process, "node", definitionmodel.WorkflowAssigneeResolver{ResolverKey: descriptor.ResolverKey, Config: map[string]any{"threshold": 1}}, 0, principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("candidate budget error=%v", err)
	}

	descriptor.MaxCandidates, descriptor.TimeoutMilliseconds = 2, 1
	timedOut := workflowProjectAssigneeResolverStub{descriptor: descriptor, resolve: func(ctx context.Context, _ runtimeext.AssigneeResolverCapabilities, _ runtimeext.AssigneeResolverContext) ([]runtimeext.AssigneeResolverCandidate, error) {
		<-ctx.Done()
		return nil, nil
	}}
	if _, err := workflowProjectResolverEngine(t, timedOut).resolveProjectAssignees(t.Context(), process, "node", definitionmodel.WorkflowAssigneeResolver{ResolverKey: descriptor.ResolverKey, Config: map[string]any{"threshold": 1}}, 0, principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("timeout error=%v", err)
	}
	time.Sleep(time.Millisecond)
}

func TestProjectAssigneeResolverReferenceValidationRejectsMissingRegistrationAndInvalidConfig(t *testing.T) {
	descriptor := workflowProjectResolverDescriptor()
	resolver := workflowProjectAssigneeResolverStub{descriptor: descriptor, resolve: func(context.Context, runtimeext.AssigneeResolverCapabilities, runtimeext.AssigneeResolverContext) ([]runtimeext.AssigneeResolverCandidate, error) {
		return nil, nil
	}}
	registry := runtimeext.NewProjectExtensionRegistry()
	if err := registry.RegisterAssigneeResolver(resolver); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	objects := func(context.Context) map[string]definitionmodel.ObjectSchema {
		return map[string]definitionmodel.ObjectSchema{"order": {Key: "order", Fields: []definitionmodel.FieldSchema{{Key: "amount"}, {Key: "region"}}}}
	}
	identity := workflowApprovalIdentityStub{roles: []identitysdk.Role{{ID: "finance", Key: "finance"}}}
	validator := NewWorkflowReferenceValidator(workflowSchemaProviderEdgeStub{}, objects, identity, registry)
	workflow := definitionmodel.WorkflowSchema{TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "record_updated", ObjectKey: "order"}}
	users, roles := validator.workflowIdentityReferenceCatalog(t.Context())
	valid := definitionmodel.WorkflowAssigneeResolver{Type: "project", ResolverKey: descriptor.ResolverKey, Config: map[string]any{"threshold": 10}}
	if issues := validator.validateWorkflowResolvers(t.Context(), workflow, "approval", []definitionmodel.WorkflowAssigneeResolver{valid}, users, roles); len(issues) != 0 {
		t.Fatalf("valid project resolver issues=%+v", issues)
	}
	missing := valid
	missing.ResolverKey = "missing"
	if issues := validator.validateWorkflowResolvers(t.Context(), workflow, "approval", []definitionmodel.WorkflowAssigneeResolver{missing}, users, roles); len(issues) != 1 || issues[0].Code != "backend.workflow.resolver_not_registered" {
		t.Fatalf("missing project resolver issues=%+v", issues)
	}
	invalid := valid
	invalid.Config = map[string]any{"unknown": true}
	if issues := validator.validateWorkflowResolvers(t.Context(), workflow, "approval", []definitionmodel.WorkflowAssigneeResolver{invalid}, users, roles); len(issues) != 1 || issues[0].Code != "backend.workflow.resolver_config_invalid" {
		t.Fatalf("invalid project resolver config issues=%+v", issues)
	}
}
