package runtimehost

import (
	"context"
	"reflect"
	"testing"

	partymodel "github.com/domainry/domainry-runtime/runtime/domain/party/model"
)

func TestRuntimeOrganizationScopeResolverBridgesPartyFacts(t *testing.T) {
	resolver := runtimeOrganizationScopeResolver(func(_ context.Context, workspaceID string, workforceProfileIDs []string) (partymodel.OrganizationScopeFacts, error) {
		if workspaceID != "workspace" || !reflect.DeepEqual(workforceProfileIDs, []string{"workforce-1"}) {
			t.Fatalf("workspace=%q profiles=%v", workspaceID, workforceProfileIDs)
		}
		return partymodel.OrganizationScopeFacts{StoreIDs: []string{"store-1"}}, nil
	})
	facts, err := resolver(t.Context(), "workspace", []string{"workforce-1"})
	if err != nil || !reflect.DeepEqual(facts.StoreIDs, []string{"store-1"}) {
		t.Fatalf("facts=%#v err=%v", facts, err)
	}
}
