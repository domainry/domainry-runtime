package capability

import (
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func TestDirectAuthoringSuccessProjectionUsesLiveInstanceAndReverseDependencies(t *testing.T) {
	service := NewCapabilityAuthoringApplicationService(nil)
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "default"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}})
	projection, err := service.DirectAuthoringSuccessProjection(t.Context(), "schema.object", principal)
	if err != nil || projection.SnapshotHash == "" || len(projection.AvailableSuccessors) == 0 {
		t.Fatalf("projection=%#v err=%v", projection, err)
	}
	foundField, foundSeed := false, false
	for _, successor := range projection.AvailableSuccessors {
		if successor.Key == "schema.field" {
			foundField = successor.Domain == "schema" && successor.Status == "supported" && successor.DetailEndpoint != "" && successor.ValidationEndpoint != ""
		}
		if successor.Key == "seed.record" {
			foundSeed = successor.Domain == "seed" && successor.Status == "supported" && successor.DetailEndpoint != "" && successor.ValidationEndpoint != ""
		}
	}
	if !foundField || !foundSeed {
		t.Fatalf("contract-derived successors missing field=%t seed=%t: %#v", foundField, foundSeed, projection.AvailableSuccessors)
	}
	if _, err := service.DirectAuthoringSuccessProjection(t.Context(), "missing", principal); apperror.CodeOf(err) != "backend.capability.not_found" {
		t.Fatalf("missing capability err=%v", err)
	}
}
