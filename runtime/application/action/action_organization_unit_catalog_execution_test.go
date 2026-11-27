package action

import (
	"context"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	organizationunit "github.com/domainry/domainry-identity-sdk/organizationunit"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
)

func TestOrganizationUnitCatalogResolvesAndIssuesExactAuthorizedNode(t *testing.T) {
	delivery := &organizationUnitDeliveryStub{resolveResult: organizationunit.DeliveredOrganizationUnit{
		OrganizationUnitID: "store-north", Code: "north", Name: "North", NodeType: organizationunit.NodeType("store"),
		Status: "active", ParentOrganizationID: "region-east", SortOrder: 1, Version: 4,
	}}
	execution := workspaceAggregateTestExecution(workspaceAggregatePrincipal("sale.aggregate"), nil, nil, new([]WorkspaceAggregateAudit))
	execution.unitOfWork = newActionTestUnitOfWork()
	execution.organizationUnitCatalogGrant = &runtimeext.OrganizationUnitCatalogCapability{NodeType: runtimeext.OrganizationUnitNodeType("store"), MaxPageSize: 25}
	execution.requestIdentity = identitysdk.RequestIdentity{AccessToken: "trusted-access-token"}
	execution.dependencies.BindOrganizationUnitDelivery = func(context.Context) (organizationunit.Delivery, error) { return delivery, nil }

	item, err := execution.ResolveOrganizationUnitCatalog(t.Context(), " store-north ")
	if err != nil {
		t.Fatal(err)
	}
	if item.ID != "store-north" || item.NodeType != runtimeext.OrganizationUnitNodeType("store") || item.Version != 4 || len(delivery.resolveRequests) != 1 {
		t.Fatalf("item=%+v requests=%+v", item, delivery.resolveRequests)
	}
	if ownerID, claim, ok := runtimeext.ResolveOrganizationUnitCatalogItem(item); !ok || ownerID != "store-north" || claim == "" {
		t.Fatalf("issued item owner=%q claim=%q ok=%v", ownerID, claim, ok)
	}
	execution.unitOfWork.rollBack(t.Context())
}

func TestOrganizationUnitCatalogListsOnlyGrantedNodeType(t *testing.T) {
	delivery := &organizationUnitDeliveryStub{listResult: organizationunit.Page{Items: []organizationunit.DeliveredOrganizationUnit{{
		OrganizationUnitID: "region-east", Code: "east", Name: "East", NodeType: organizationunit.NodeType("region"),
		Status: "active", ParentOrganizationID: "company", Version: 1,
	}}}}
	execution := workspaceAggregateTestExecution(workspaceAggregatePrincipal("sale.aggregate"), nil, nil, new([]WorkspaceAggregateAudit))
	execution.unitOfWork = newActionTestUnitOfWork()
	execution.organizationUnitCatalogGrant = &runtimeext.OrganizationUnitCatalogCapability{NodeType: runtimeext.OrganizationUnitNodeType("region"), MaxPageSize: 10}
	execution.requestIdentity = identitysdk.RequestIdentity{AccessToken: "trusted-access-token"}
	execution.dependencies.BindOrganizationUnitDelivery = func(context.Context) (organizationunit.Delivery, error) { return delivery, nil }

	page, err := execution.ListOrganizationUnits(t.Context(), runtimeext.OrganizationUnitCatalogRequest{PageSize: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != "region-east" || len(delivery.listRequests) != 1 || delivery.listRequests[0].NodeType != organizationunit.NodeType("region") {
		t.Fatalf("page=%+v requests=%+v", page, delivery.listRequests)
	}
	execution.unitOfWork.rollBack(t.Context())
}
