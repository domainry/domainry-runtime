package action

import (
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	organizationunit "github.com/domainry/domainry-identity-sdk/organizationunit"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
)

func TestResolveSoleAuthorizedActiveOrganizationIsNodeTypeAgnosticAndFailsClosed(t *testing.T) {
	execution := &businessActionExecution{}
	for _, test := range []struct {
		name     string
		page     organizationunit.Page
		wantID   string
		wantCode string
	}{
		{name: "one active region", page: organizationunit.Page{Items: []organizationunit.DeliveredOrganizationUnit{{OrganizationUnitID: "region-east", NodeType: organizationunit.NodeType("region"), Status: "active"}}}, wantID: "region-east"},
		{name: "two regions", page: organizationunit.Page{Items: []organizationunit.DeliveredOrganizationUnit{{OrganizationUnitID: "region-east", NodeType: organizationunit.NodeType("region"), Status: "active"}, {OrganizationUnitID: "region-west", NodeType: organizationunit.NodeType("region"), Status: "active"}}}, wantCode: "backend.action.target_organization_ambiguous"},
		{name: "continuation", page: organizationunit.Page{Items: []organizationunit.DeliveredOrganizationUnit{{OrganizationUnitID: "region-east", NodeType: organizationunit.NodeType("region"), Status: "active"}}, NextCursor: "more"}, wantCode: "backend.action.target_organization_ambiguous"},
		{name: "disabled", page: organizationunit.Page{Items: []organizationunit.DeliveredOrganizationUnit{{OrganizationUnitID: "region-east", NodeType: organizationunit.NodeType("region"), Status: "disabled"}}}, wantCode: "backend.action.target_organization_ambiguous"},
	} {
		t.Run(test.name, func(t *testing.T) {
			delivery := &organizationUnitDeliveryStub{listResult: test.page}
			id, err := execution.resolveSoleAuthorizedActiveOrganization(t.Context(), delivery, runtimeext.OrganizationUnitNodeType("region"))
			if id != test.wantID || (test.wantCode == "" && err != nil) || (test.wantCode != "" && apperror.CodeOf(err) != test.wantCode) {
				t.Fatalf("id=%q err=%v", id, err)
			}
		})
	}
}
