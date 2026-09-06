package action

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	aggregatecontract "github.com/domainry/domainry-runtime/runtime/domain/workspaceaggregate/contract"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

type workspaceAggregateCatalogStub struct {
	workspaces []aggregatecontract.Workspace
	err        error
	calls      int
}

func workspaceAggregatePrincipal(permissions ...string) principalmodel.Principal {
	return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}, accessfixture.Bundle{
		Key: "tenant_admin", Permissions: permissions,
		DataPolicies:  accessfixture.DataPoliciesForPermissions(permissions, identitysdk.DataScopeAll),
		FieldPolicies: []accessfixture.FieldPolicyFixture{{ObjectKey: "sale", FieldKey: "status", Read: true}, {ObjectKey: "sale", FieldKey: "amount", Read: true}},
	})
}

func (stub *workspaceAggregateCatalogStub) ListActive(context.Context, principalmodel.SystemScope, int) ([]aggregatecontract.Workspace, error) {
	stub.calls++
	return append([]aggregatecontract.Workspace(nil), stub.workspaces...), stub.err
}

type workspaceAggregateRepositoryStub struct {
	result aggregatecontract.Result
	err    error
	calls  int
	query  aggregatecontract.Query
}

func (stub *workspaceAggregateRepositoryStub) Aggregate(_ context.Context, query aggregatecontract.Query) (aggregatecontract.Result, error) {
	stub.calls++
	stub.query = query
	return stub.result, stub.err
}

func workspaceAggregateTestExecution(principal principalmodel.Principal, catalog aggregatecontract.Catalog, repository aggregatecontract.Repository, audits *[]WorkspaceAggregateAudit) *businessActionExecution {
	object := definitionmodel.ObjectSchema{Key: "sale", Fields: []definitionmodel.FieldSchema{{Key: "status", Type: "select"}, {Key: "amount", Type: "currency"}}}
	return &businessActionExecution{
		dependencies: BusinessHandlerExecutionDependencies{
			ObjectForKey: func(string) (definitionmodel.ObjectSchema, bool) { return object, true },
			NormalizeAggregateQuery: func(_ definitionmodel.ObjectSchema, query recordmodel.RecordListQuery, _ principalmodel.Principal) recordmodel.RecordListQuery {
				query.AuthorizationMode = recordmodel.RecordQueryAuthorizationUnrestricted
				return query
			},
			WorkspaceAggregateCatalog: catalog, WorkspaceAggregateRepository: repository,
			AuditWorkspaceAggregate: func(_ context.Context, value WorkspaceAggregateAudit) error {
				*audits = append(*audits, value)
				return nil
			},
		},
		invocation: actionmodel.ActionInvocation{Principal: principal},
		action:     definitionmodel.ActionSchema{Key: "sale.aggregate", ObjectKey: "sale", EffectSet: &definitionmodel.ActionEffectSet{Read: []definitionmodel.ActionObjectEffect{{ObjectKey: "sale"}}}},
		unitOfWork: &actionUnitOfWork{phases: newActionExecutionPhaseMachine()},
		aggregateGrants: []runtimeext.CrossWorkspaceAggregateCapability{{
			Key: "daily", ObjectKey: "sale", Dimensions: []runtimeext.CrossWorkspaceAggregateDimension{{Key: "workspace", Field: runtimeext.CrossWorkspaceDimensionWorkspace}},
			Measures:      []runtimeext.CrossWorkspaceAggregateMeasure{{Key: "total", Operation: runtimeext.AggregateSum, Field: "amount"}},
			Filters:       []runtimeext.CrossWorkspaceAggregateFilterCapability{{Field: "status", Operators: []string{"eq"}}},
			MaxWorkspaces: 2, MaxSourceRows: 100, MaxResultRows: 10, TimeoutMilliseconds: 100,
		}},
	}
}

func TestCrossWorkspaceAggregateRequiresExactActionPermissionBeforePersistence(t *testing.T) {
	catalog := &workspaceAggregateCatalogStub{workspaces: []aggregatecontract.Workspace{{ID: "workspace-a", CanonicalCode: "a"}}}
	repository := &workspaceAggregateRepositoryStub{}
	audits := []WorkspaceAggregateAudit{}
	execution := workspaceAggregateTestExecution(workspaceAggregatePrincipal("sale.read"), catalog, repository, &audits)
	_, err := execution.CrossWorkspaceAggregate(t.Context(), runtimeext.CrossWorkspaceAggregateRequest{CapabilityKey: "daily"})
	if apperror.CodeOf(err) != "backend.action.cross_workspace_aggregate_permission_denied" || catalog.calls != 0 || repository.calls != 0 {
		t.Fatalf("code=%s catalog=%d repository=%d error=%v", apperror.CodeOf(err), catalog.calls, repository.calls, err)
	}
	if len(audits) != 1 || audits[0].Outcome != "denied" || audits[0].SourceRowCount != 0 || audits[0].ResultRowCount != 0 {
		t.Fatalf("audit=%#v", audits)
	}
}

func TestCrossWorkspaceAggregateUsesFrozenGrantAndOpaqueWorkspaceProjection(t *testing.T) {
	catalog := &workspaceAggregateCatalogStub{workspaces: []aggregatecontract.Workspace{{ID: "workspace-a", CanonicalCode: "north"}, {ID: "workspace-b", CanonicalCode: "south"}}}
	repository := &workspaceAggregateRepositoryStub{result: aggregatecontract.Result{Rows: []map[string]string{{"workspace": "north", "total": "12.30"}, {"workspace": "south", "total": "7.70"}}, SourceRowCount: 4}}
	audits := []WorkspaceAggregateAudit{}
	execution := workspaceAggregateTestExecution(workspaceAggregatePrincipal("sale.aggregate"), catalog, repository, &audits)
	result, err := execution.CrossWorkspaceAggregate(t.Context(), runtimeext.CrossWorkspaceAggregateRequest{CapabilityKey: "daily", Filters: []runtimeext.CrossWorkspaceAggregateFilter{{Field: "status", Operator: "eq", Value: "paid"}}, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if result.WorkspaceCount != 2 || result.SourceRowCount != 4 || len(result.Rows) != 2 || result.Rows[0].Values["workspace"] != "north" || repository.calls != 1 {
		t.Fatalf("result=%#v calls=%d", result, repository.calls)
	}
	if repository.query.MaxResultRows != 5 || repository.query.RecordQuery.FilterExpression == nil || repository.query.RecordQuery.FilterExpression.Field != "status" {
		t.Fatalf("query=%#v", repository.query)
	}
	if len(audits) != 1 || audits[0].Outcome != "success" || audits[0].ScopeSHA256 == "" || audits[0].WorkspaceCount != 2 || audits[0].SourceRowCount != 4 || audits[0].ResultRowCount != 2 {
		t.Fatalf("audit=%#v", audits)
	}
}

func TestCrossWorkspaceAggregateFailsClosedForGrantPhaseLimitsAndTimeout(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*businessActionExecution, *workspaceAggregateCatalogStub, *workspaceAggregateRepositoryStub)
		request runtimeext.CrossWorkspaceAggregateRequest
		code    string
	}{
		{name: "grant", request: runtimeext.CrossWorkspaceAggregateRequest{CapabilityKey: "other"}, code: "backend.action.cross_workspace_aggregate_grant_denied"},
		{name: "physical field", request: runtimeext.CrossWorkspaceAggregateRequest{CapabilityKey: "daily", Filters: []runtimeext.CrossWorkspaceAggregateFilter{{Field: "workspace_id", Operator: "eq", Value: "workspace-a"}}}, code: "backend.action.cross_workspace_aggregate_filter_invalid"},
		{name: "date bucket non-temporal field", prepare: func(execution *businessActionExecution, _ *workspaceAggregateCatalogStub, _ *workspaceAggregateRepositoryStub) {
			execution.aggregateGrants[0].Dimensions = []runtimeext.CrossWorkspaceAggregateDimension{{Key: "hour", Field: "status", Transform: &runtimeext.CrossWorkspaceAggregateDimensionTransform{DateBucket: &runtimeext.CrossWorkspaceAggregateDateBucketTransform{Grain: "hour", TimeZone: "Asia/Tokyo"}}}}
		}, request: runtimeext.CrossWorkspaceAggregateRequest{CapabilityKey: "daily"}, code: "backend.action.cross_workspace_aggregate_field_denied"},
		{name: "phase", prepare: func(execution *businessActionExecution, _ *workspaceAggregateCatalogStub, _ *workspaceAggregateRepositoryStub) {
			_ = execution.unitOfWork.phases.beginWriting()
		}, request: runtimeext.CrossWorkspaceAggregateRequest{CapabilityKey: "daily"}, code: "backend.action.cross_workspace_aggregate_phase_forbidden"},
		{name: "workspace limit", prepare: func(_ *businessActionExecution, catalog *workspaceAggregateCatalogStub, _ *workspaceAggregateRepositoryStub) {
			catalog.workspaces = append(catalog.workspaces, aggregatecontract.Workspace{ID: "workspace-c", CanonicalCode: "c"})
		}, request: runtimeext.CrossWorkspaceAggregateRequest{CapabilityKey: "daily"}, code: "backend.action.cross_workspace_aggregate_workspace_limit_exceeded"},
		{name: "source limit", prepare: func(_ *businessActionExecution, _ *workspaceAggregateCatalogStub, repository *workspaceAggregateRepositoryStub) {
			repository.err = &aggregatecontract.LimitExceededError{Kind: aggregatecontract.LimitSourceRows}
		}, request: runtimeext.CrossWorkspaceAggregateRequest{CapabilityKey: "daily"}, code: "backend.action.cross_workspace_aggregate_source_limit_exceeded"},
		{name: "result limit", prepare: func(_ *businessActionExecution, _ *workspaceAggregateCatalogStub, repository *workspaceAggregateRepositoryStub) {
			repository.err = &aggregatecontract.LimitExceededError{Kind: aggregatecontract.LimitResultRows}
		}, request: runtimeext.CrossWorkspaceAggregateRequest{CapabilityKey: "daily"}, code: "backend.action.cross_workspace_aggregate_result_limit_exceeded"},
		{name: "timeout", prepare: func(_ *businessActionExecution, catalog *workspaceAggregateCatalogStub, _ *workspaceAggregateRepositoryStub) {
			catalog.err = context.DeadlineExceeded
		}, request: runtimeext.CrossWorkspaceAggregateRequest{CapabilityKey: "daily"}, code: "backend.action.cross_workspace_aggregate_timeout"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			catalog := &workspaceAggregateCatalogStub{workspaces: []aggregatecontract.Workspace{{ID: "workspace-a", CanonicalCode: "a"}, {ID: "workspace-b", CanonicalCode: "b"}}}
			repository := &workspaceAggregateRepositoryStub{}
			audits := []WorkspaceAggregateAudit{}
			execution := workspaceAggregateTestExecution(workspaceAggregatePrincipal("sale.aggregate"), catalog, repository, &audits)
			if test.prepare != nil {
				test.prepare(execution, catalog, repository)
			}
			_, err := execution.CrossWorkspaceAggregate(t.Context(), test.request)
			if apperror.CodeOf(err) != test.code {
				t.Fatalf("code=%s want=%s error=%v", apperror.CodeOf(err), test.code, err)
			}
			if len(audits) != 1 {
				t.Fatalf("audits=%#v", audits)
			}
		})
	}
}

func TestCrossWorkspaceAggregateTimeoutStopsRepository(t *testing.T) {
	catalog := &workspaceAggregateCatalogStub{workspaces: []aggregatecontract.Workspace{{ID: "workspace-a", CanonicalCode: "a"}}}
	repository := &workspaceAggregateRepositoryStub{err: context.DeadlineExceeded}
	audits := []WorkspaceAggregateAudit{}
	execution := workspaceAggregateTestExecution(workspaceAggregatePrincipal("sale.aggregate"), catalog, repository, &audits)
	execution.aggregateGrants[0].TimeoutMilliseconds = int((time.Millisecond).Milliseconds())
	_, err := execution.CrossWorkspaceAggregate(t.Context(), runtimeext.CrossWorkspaceAggregateRequest{CapabilityKey: "daily"})
	if !errors.Is(err, context.DeadlineExceeded) || apperror.CodeOf(err) != "backend.action.cross_workspace_aggregate_timeout" {
		t.Fatalf("error=%v", err)
	}
}

func TestCrossWorkspaceAggregateReturnsNoRowsWhenRequiredAuditFails(t *testing.T) {
	catalog := &workspaceAggregateCatalogStub{workspaces: []aggregatecontract.Workspace{{ID: "workspace-a", CanonicalCode: "a"}}}
	repository := &workspaceAggregateRepositoryStub{result: aggregatecontract.Result{Rows: []map[string]string{{"workspace": "a", "total": "3.00"}}, SourceRowCount: 1}}
	audits := []WorkspaceAggregateAudit{}
	execution := workspaceAggregateTestExecution(workspaceAggregatePrincipal("sale.aggregate"), catalog, repository, &audits)
	execution.dependencies.AuditWorkspaceAggregate = func(context.Context, WorkspaceAggregateAudit) error { return errors.New("audit unavailable") }
	result, err := execution.CrossWorkspaceAggregate(t.Context(), runtimeext.CrossWorkspaceAggregateRequest{CapabilityKey: "daily"})
	if apperror.CodeOf(err) != "backend.action.cross_workspace_aggregate_audit_failed" || len(result.Rows) != 0 || repository.calls != 1 {
		t.Fatalf("result=%#v calls=%d error=%v", result, repository.calls, err)
	}
}
