package capability

import (
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestDirectAuthoringSuccessProjectionUsesLiveInstanceAndReverseDependencies(t *testing.T) {
	service := NewCapabilityAuthoringApplicationService(nil)
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{Permissions: []string{"runtime.appschema.validate_application_definition"}})
	projection, err := service.DirectAuthoringSuccessProjection(t.Context(), "schema.object", principal)
	if err != nil || projection.SnapshotHash == "" || len(projection.AvailableSuccessors) == 0 {
		t.Fatalf("projection=%#v err=%v", projection, err)
	}
	foundField := false
	for _, successor := range projection.AvailableSuccessors {
		if successor.DetailService != "plane" || !strings.HasPrefix(successor.DetailEndpoint, "/capabilities/authoring-contracts/") {
			t.Fatalf("successor does not point to Plane: %#v", successor)
		}
		if successor.Key == "schema.field" {
			foundField = successor.Domain == "schema" && successor.Status == "supported" && successor.DetailEndpoint != "" && successor.ValidationEndpoint != ""
		}
	}
	if !foundField {
		t.Fatalf("contract-derived field successor is missing: %#v", projection.AvailableSuccessors)
	}
	if _, err := service.DirectAuthoringSuccessProjection(t.Context(), "missing", principal); apperror.CodeOf(err) != "backend.capability.not_found" {
		t.Fatalf("missing capability err=%v", err)
	}
}
