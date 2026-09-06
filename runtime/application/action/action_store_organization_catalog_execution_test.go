package action

import (
	"context"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

type storeOrganizationCatalogDeliveryStub struct {
	requests       []identitysdk.StoreOrganizationListRequest
	resolveRequest identitysdk.StoreOrganizationResolveRequest
	resolveResult  identitysdk.StoreOrganization
	resolveCalls   int
	pages          []identitysdk.StoreOrganizationPage
	err            error
}

func (*storeOrganizationCatalogDeliveryStub) DeliverStoreOrganization(context.Context, identitysdk.StoreOrganizationDeliveryRequest) (identitysdk.StoreOrganizationDeliveryResult, error) {
	panic("generic Organization mutation is not exposed by the Runtime catalog capability")
}

func (stub *storeOrganizationCatalogDeliveryStub) ResolveStoreOrganization(_ context.Context, request identitysdk.StoreOrganizationResolveRequest) (identitysdk.StoreOrganization, error) {
	stub.resolveCalls++
	stub.resolveRequest = request
	return stub.resolveResult, stub.err
}

func TestStoreOrganizationCatalogResolvesExactAuthorizedOrganization(t *testing.T) {
	delivery := &storeOrganizationCatalogDeliveryStub{resolveResult: identitysdk.StoreOrganization{
		ID: "store-north", Code: "north", Name: "North", Status: "active", ParentOrganizationID: "company-hq", SortOrder: 1, Version: 4,
	}}
	execution := workspaceAggregateTestExecution(workspaceAggregatePrincipal("sale.aggregate"), nil, nil, new([]WorkspaceAggregateAudit))
	execution.unitOfWork = newActionTestUnitOfWork()
	execution.storeCatalogGrant = &runtimeext.StoreOrganizationCatalogCapability{MaxPageSize: 25}
	execution.requestIdentity = identitysdk.RequestIdentity{AccessToken: "trusted-access-token"}
	execution.dependencies.BindStoreOrganizationDelivery = func(context.Context) (identitysdk.StoreOrganizationDelivery, error) { return delivery, nil }

	item, err := execution.ResolveStoreOrganization(t.Context(), " store-north ")
	if err != nil {
		t.Fatal(err)
	}
	if item.ID != "store-north" || item.Version != 4 || delivery.resolveCalls != 1 || delivery.resolveRequest.OrganizationID != "store-north" || delivery.resolveRequest.AccessToken != "trusted-access-token" {
		t.Fatalf("item=%+v request=%+v calls=%d", item, delivery.resolveRequest, delivery.resolveCalls)
	}
	if ownerID, claim, ok := runtimeext.ResolveStoreOrganizationCatalogItem(item); !ok || ownerID != "store-north" || claim == "" {
		t.Fatalf("issued item owner=%q claim=%q ok=%v", ownerID, claim, ok)
	}
	execution.unitOfWork.rollBack(t.Context())

	delivery.resolveResult.ID = "store-south"
	mismatch := workspaceAggregateTestExecution(workspaceAggregatePrincipal("sale.aggregate"), nil, nil, new([]WorkspaceAggregateAudit))
	mismatch.unitOfWork = newActionTestUnitOfWork()
	mismatch.storeCatalogGrant = &runtimeext.StoreOrganizationCatalogCapability{MaxPageSize: 25}
	mismatch.requestIdentity = identitysdk.RequestIdentity{AccessToken: "trusted-access-token"}
	mismatch.dependencies.BindStoreOrganizationDelivery = func(context.Context) (identitysdk.StoreOrganizationDelivery, error) { return delivery, nil }
	if _, err := mismatch.ResolveStoreOrganization(t.Context(), "store-north"); apperror.CodeOf(err) != "backend.action.store_organization_catalog_result_invalid" {
		t.Fatalf("mismatched resolve error=%v", err)
	}
	mismatch.unitOfWork.rollBack(t.Context())
}

func (stub *storeOrganizationCatalogDeliveryStub) ListStoreOrganizations(_ context.Context, request identitysdk.StoreOrganizationListRequest) (identitysdk.StoreOrganizationPage, error) {
	stub.requests = append(stub.requests, request)
	if stub.err != nil {
		return identitysdk.StoreOrganizationPage{}, stub.err
	}
	index := len(stub.requests) - 1
	if index >= len(stub.pages) {
		return identitysdk.StoreOrganizationPage{}, nil
	}
	return stub.pages[index], nil
}

func TestStoreOrganizationCatalogUsesTrustedIdentityScopeAndSameActionSnapshot(t *testing.T) {
	delivery := &storeOrganizationCatalogDeliveryStub{pages: []identitysdk.StoreOrganizationPage{
		{Items: []identitysdk.StoreOrganization{{ID: "store-north", Code: "north", Name: "North", Status: "active", ParentOrganizationID: "company-hq", Path: "/company-hq/store-north", AncestorIDs: []string{"company-hq"}, Depth: 2, SortOrder: 1, Version: 3}}, NextCursor: "opaque-next"},
		{Items: []identitysdk.StoreOrganization{{ID: "store-south", Code: "south", Name: "South", Status: "active", ParentOrganizationID: "company-hq", SortOrder: 2, Version: 1}}},
	}}
	execution := workspaceAggregateTestExecution(workspaceAggregatePrincipal("sale.aggregate"), nil, nil, new([]WorkspaceAggregateAudit))
	execution.unitOfWork = newActionTestUnitOfWork()
	execution.storeCatalogGrant = &runtimeext.StoreOrganizationCatalogCapability{MaxPageSize: 25}
	execution.requestIdentity = identitysdk.RequestIdentity{AccessToken: "trusted-access-token"}
	execution.dependencies.BindStoreOrganizationDelivery = func(ctx context.Context) (identitysdk.StoreOrganizationDelivery, error) {
		if ctx.Value(actionUnitOfWorkTransactionContextKey{}) != true || execution.Phase() != runtimeext.ExecutionPhaseWriting {
			t.Fatalf("Identity catalog was not bound inside the Action transaction: phase=%s", execution.Phase())
		}
		return delivery, nil
	}
	recordReadInTransaction := false
	execution.dependencies.ListRecords = func(ctx context.Context, _ string, query recordmodel.RecordListQuery, _ principalmodel.Principal) (recordmodel.RecordPageResult, error) {
		recordReadInTransaction = ctx.Value(actionUnitOfWorkTransactionContextKey{}) == true
		if query.OwnerOrganizationScopeID != "store-north" {
			t.Fatalf("owner Organization scope=%q", query.OwnerOrganizationScopeID)
		}
		return recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "back-north", OwnerOrgID: "store-north"}}, Total: 1}, nil
	}

	first, err := execution.ListStoreOrganizations(t.Context(), runtimeext.StoreOrganizationCatalogRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 1 || first.Items[0].ID != "store-north" || first.NextCursor != "opaque-next" || delivery.requests[0].PageSize != 25 || delivery.requests[0].AccessToken != "trusted-access-token" {
		t.Fatalf("first=%#v request=%#v", first, delivery.requests[0])
	}
	// The issued catalog item narrows the record query without exposing the
	// owner_org_id system field as a Handler-authored filter.
	if _, err := execution.QueryRecords(t.Context(), runtimeext.RecordQuery{Operation: runtimeext.QueryList, ObjectKey: "sale", StoreOrganization: &first.Items[0], Limit: 10}); err != nil {
		t.Fatal(err)
	}
	if !recordReadInTransaction {
		t.Fatal("Runtime record projection did not share the catalog Action snapshot")
	}
	second, err := execution.ListStoreOrganizations(t.Context(), runtimeext.StoreOrganizationCatalogRequest{PageSize: 10, Cursor: first.NextCursor})
	if err != nil || len(second.Items) != 1 || delivery.requests[1].Cursor != "opaque-next" || delivery.requests[1].PageSize != 10 {
		t.Fatalf("second=%#v requests=%#v err=%v", second, delivery.requests, err)
	}
	execution.unitOfWork.rollBack(t.Context())
}

func TestStoreOrganizationRecordLookupUsesIssuedIdentityNotStoreNameAndFailsClosed(t *testing.T) {
	delivery := &storeOrganizationCatalogDeliveryStub{pages: []identitysdk.StoreOrganizationPage{{Items: []identitysdk.StoreOrganization{
		{ID: "store-north", Code: "north", Name: "Same Name", Status: "active", ParentOrganizationID: "company-hq"},
		{ID: "store-south", Code: "south", Name: "Same Name", Status: "active", ParentOrganizationID: "company-hq"},
	}}}}
	execution := workspaceAggregateTestExecution(workspaceAggregatePrincipal("sale.aggregate"), nil, nil, new([]WorkspaceAggregateAudit))
	execution.unitOfWork = newActionTestUnitOfWork()
	execution.storeCatalogGrant = &runtimeext.StoreOrganizationCatalogCapability{MaxPageSize: 25}
	execution.requestIdentity = identitysdk.RequestIdentity{AccessToken: "trusted-access-token"}
	execution.dependencies.BindStoreOrganizationDelivery = func(context.Context) (identitysdk.StoreOrganizationDelivery, error) { return delivery, nil }
	seenScopes := []string{}
	execution.dependencies.ListRecords = func(_ context.Context, _ string, query recordmodel.RecordListQuery, _ principalmodel.Principal) (recordmodel.RecordPageResult, error) {
		seenScopes = append(seenScopes, query.OwnerOrganizationScopeID)
		return recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "config-" + query.OwnerOrganizationScopeID, OwnerOrgID: query.OwnerOrganizationScopeID}}, Total: 1}, nil
	}
	page, err := execution.ListStoreOrganizations(t.Context(), runtimeext.StoreOrganizationCatalogRequest{PageSize: 25})
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("page=%#v err=%v", page, err)
	}
	for index := range page.Items {
		result, queryErr := execution.QueryRecords(t.Context(), runtimeext.RecordQuery{Operation: runtimeext.QueryList, ObjectKey: "sale", StoreOrganization: &page.Items[index], Limit: 2})
		if queryErr != nil || len(result.Records) != 1 {
			t.Fatalf("store[%d] result=%#v err=%v", index, result, queryErr)
		}
	}
	if len(seenScopes) != 2 || seenScopes[0] != "store-north" || seenScopes[1] != "store-south" {
		t.Fatalf("record scopes=%v", seenScopes)
	}

	tampered := page.Items[0]
	tampered.ID = "store-south"
	if _, err := execution.QueryRecords(t.Context(), runtimeext.RecordQuery{Operation: runtimeext.QueryList, ObjectKey: "sale", StoreOrganization: &tampered, Limit: 2}); apperror.CodeOf(err) != "backend.action.query_invalid" {
		t.Fatalf("tampered reference error=%v", err)
	}
	unregistered, _, err := runtimeext.IssueStoreOrganizationCatalogItem(runtimeext.StoreOrganizationCatalogItem{ID: "store-north"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := execution.QueryRecords(t.Context(), runtimeext.RecordQuery{Operation: runtimeext.QueryList, ObjectKey: "sale", StoreOrganization: &unregistered, Limit: 2}); apperror.CodeOf(err) != "backend.action.store_organization_record_reference_denied" {
		t.Fatalf("unregistered reference error=%v", err)
	}

	execution.dependencies.ListRecords = func(_ context.Context, _ string, _ recordmodel.RecordListQuery, _ principalmodel.Principal) (recordmodel.RecordPageResult, error) {
		return recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "foreign", OwnerOrgID: "store-foreign"}}, Total: 1}, nil
	}
	if _, err := execution.QueryRecords(t.Context(), runtimeext.RecordQuery{Operation: runtimeext.QueryList, ObjectKey: "sale", StoreOrganization: &page.Items[0], Limit: 2}); apperror.CodeOf(err) != "backend.action.store_organization_record_scope_violation" {
		t.Fatalf("foreign record error=%v", err)
	}
	execution.unitOfWork.rollBack(t.Context())
}

func TestStoreOrganizationCatalogFailsClosedForGrantLimitAndIdentityDenial(t *testing.T) {
	delivery := &storeOrganizationCatalogDeliveryStub{err: &identitysdk.Error{Code: "backend.identity.store_organization_projection_denied"}}
	execution := workspaceAggregateTestExecution(workspaceAggregatePrincipal("sale.aggregate"), nil, nil, new([]WorkspaceAggregateAudit))
	execution.unitOfWork = newActionTestUnitOfWork()
	execution.requestIdentity = identitysdk.RequestIdentity{AccessToken: "trusted-access-token"}
	execution.dependencies.BindStoreOrganizationDelivery = func(context.Context) (identitysdk.StoreOrganizationDelivery, error) { return delivery, nil }
	if _, err := execution.ListStoreOrganizations(t.Context(), runtimeext.StoreOrganizationCatalogRequest{}); apperror.CodeOf(err) != "backend.action.store_organization_catalog_grant_denied" {
		t.Fatalf("missing grant error=%v", err)
	}
	execution.storeCatalogGrant = &runtimeext.StoreOrganizationCatalogCapability{MaxPageSize: 20}
	if _, err := execution.ListStoreOrganizations(t.Context(), runtimeext.StoreOrganizationCatalogRequest{PageSize: 21}); apperror.CodeOf(err) != "backend.action.store_organization_catalog_page_size_invalid" {
		t.Fatalf("page limit error=%v", err)
	}
	if _, err := execution.ListStoreOrganizations(t.Context(), runtimeext.StoreOrganizationCatalogRequest{PageSize: 20}); apperror.CodeOf(err) != "backend.identity.store_organization_projection_denied" {
		t.Fatalf("Identity scope denial error=%v", err)
	}
	execution.unitOfWork.rollBack(t.Context())
}
