// Frontend-capability application service tests.
package deployment

import (
	"context"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"

	"testing"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func frontendCapabilityTestService() *DeploymentFrontendCapabilityApplicationService {
	return frontendCapabilityTestServiceWithBindings(nil)
}

func frontendCapabilityTestServiceWithBindings(bindings func(context.Context) FrontendBusinessBindings) *DeploymentFrontendCapabilityApplicationService {
	return NewDeploymentFrontendCapabilityApplicationService(FrontendCapabilityDependencies{
		Repository:       newDeploymentFrontendCapabilityTestStore(),
		ContractVersion:  "runtime-authoring-v1",
		BusinessBindings: bindings,
		Capabilities: func() []deploymentmodel.FrontendCapabilityDefinition {
			return []deploymentmodel.FrontendCapabilityDefinition{{Key: "schema.field", FrontendSupportKey: "metadata.field.editor.v1", Permissions: []string{"workspace.admin"}}}
		},
	})
}

func frontendCapabilityAdmin(workspace string) principalmodel.Principal {
	return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: workspace, UserID: "admin"}}, accessfixture.Bundle{Key: "admin", Permissions: []string{"workspace.admin"}})
}

func frontendCapabilityEvidence() *deploymentmodel.FrontendDeploymentEvidence {
	return &deploymentmodel.FrontendDeploymentEvidence{
		AuditContractVersion: "domainry-frontend-evidence-v1",
		DesignContractHash:   "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		RouteRegistryHash:    "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		FrontendSourceHash:   "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		AuditArtifactHash:    "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
	}
}

func TestFrontendCapabilityServiceOwnsWorkspaceState(t *testing.T) {
	service := frontendCapabilityTestService()
	manifest := deploymentmodel.FrontendCapabilityManifest{
		ManifestVersion: deploymentmodel.FrontendCapabilityManifestVersion, FrontendVersion: "domainry-admin-1.0.0", RuntimeContractVersions: []string{"runtime-authoring-v1"},
		Entries: []deploymentmodel.FrontendCapabilitySupportEntry{{SupportKey: "metadata.field.editor.v1", CapabilityKeys: []string{"schema.field"}, Route: "/system/metadata", RequiredPermissions: []string{"workspace.admin"}, FeatureModule: "features/system/metadata.tsx", AcceptanceTests: []string{"tests/e2e/metadata.spec.ts"}}},
	}
	registered, err := service.RegisterManifest(t.Context(), manifest, frontendCapabilityAdmin("workspace-a"))
	if err != nil || registered.Manifest == nil {
		t.Fatalf("register snapshot=%#v err=%v", registered, err)
	}
	if current, err := service.Snapshot(t.Context(), frontendCapabilityAdmin("workspace-a")); err != nil || current.ManifestHash != registered.ManifestHash {
		t.Fatalf("current=%#v err=%v", current, err)
	}
	replayed, err := service.RegisterManifest(t.Context(), manifest, frontendCapabilityAdmin("workspace-a"))
	if err != nil || replayed.Revision != registered.Revision || replayed.UpdatedAt != registered.UpdatedAt {
		t.Fatalf("identical registration advanced state: first=%#v replay=%#v err=%v", registered, replayed, err)
	}
	if other, err := service.Snapshot(t.Context(), frontendCapabilityAdmin("workspace-b")); err != nil || other.Status != "unknown" || other.Manifest != nil {
		t.Fatalf("workspace state leaked: %#v err=%v", other, err)
	}
}

func TestFrontendCapabilityServiceRejectsInvalidCapabilityAndBusinessBindings(t *testing.T) {
	service := frontendCapabilityTestService()
	invented := deploymentmodel.FrontendCapabilityManifest{ManifestVersion: deploymentmodel.FrontendCapabilityManifestVersion, FrontendVersion: "1", RuntimeContractVersions: []string{"runtime-authoring-v1"}, Entries: []deploymentmodel.FrontendCapabilitySupportEntry{{SupportKey: "metadata.field.editor.v1", CapabilityKeys: []string{"schema.invented"}, Route: "/system/metadata", RequiredPermissions: []string{"workspace.admin"}, FeatureModule: "metadata.tsx", AcceptanceTests: []string{"metadata.spec.ts"}}}}
	if _, err := service.ValidateManifestDefinition(invented); err == nil {
		t.Fatal("invented capability binding was accepted")
	}
	bindings := FrontendBusinessBindings{Objects: map[string]bool{"customer": true}, Actions: map[string]bool{}, Reports: map[string]bool{}, Fields: map[string]bool{}}
	service = frontendCapabilityTestServiceWithBindings(func(context.Context) FrontendBusinessBindings { return bindings })
	invalid := invented
	invalid.Entries[0].CapabilityKeys = []string{"schema.field"}
	invalid.Entries[0].BusinessObjects = []string{"customer", "customer"}
	invalid.Entries[0].FieldKeys = []string{"customer.missing"}
	result, _ := service.ValidateManifest(t.Context(), invalid, frontendCapabilityAdmin("workspace-a"))
	if result.Valid {
		t.Fatalf("invalid domain bindings passed: %#v", result)
	}
}

func TestFrontendCapabilityBusinessRouteEvidenceRequiresHashes(t *testing.T) {
	service := frontendCapabilityTestService()
	manifest := deploymentmodel.FrontendCapabilityManifest{
		ManifestVersion: deploymentmodel.FrontendCapabilityManifestVersion, FrontendVersion: "1", RuntimeContractVersions: []string{"runtime-authoring-v1"},
		Entries: []deploymentmodel.FrontendCapabilitySupportEntry{{SupportKey: "domain.route.customer.v1", Route: "/customers", FeatureModule: "features/customers.tsx", AcceptanceTests: []string{"tests/e2e/customers.spec.ts"}, ActorRoles: []string{"sales"}, BusinessObjects: []string{"customer"}, ImplementedActions: []string{"customer.update"}, AcceptanceClaims: []string{"sales_updates_customer"}}},
	}
	result, _ := service.ValidateManifest(t.Context(), manifest, frontendCapabilityAdmin("workspace-a"))
	if result.Valid {
		t.Fatal("domain route without deployment hashes passed")
	}
	manifest.DeploymentEvidence = frontendCapabilityEvidence()
	result, _ = service.ValidateManifest(t.Context(), manifest, frontendCapabilityAdmin("workspace-a"))
	if !result.Valid {
		t.Fatalf("domain route evidence rejected: %#v", result.Issues)
	}
	snapshot := service.SnapshotManifest(result.NormalizedManifest)
	if snapshot.ManifestHash == "" || snapshot.Manifest == nil {
		t.Fatalf("snapshot lacks manifest evidence: %#v", snapshot)
	}
}

func TestFrontendCapabilitySnapshotPublishesRuntimeAndSchemaDiagnostics(t *testing.T) {
	service := frontendCapabilityTestServiceWithBindings(func(context.Context) FrontendBusinessBindings {
		return FrontendBusinessBindings{SchemaHash: "schema-hash", SchemaSnapshotVersion: "schema-snapshot-7"}
	})
	manifest := deploymentmodel.FrontendCapabilityManifest{
		ManifestVersion:         deploymentmodel.FrontendCapabilityManifestVersion,
		FrontendVersion:         "domainry-admin-1.0.0",
		RuntimeContractVersions: []string{"runtime-authoring-v1"},
		Entries: []deploymentmodel.FrontendCapabilitySupportEntry{{
			SupportKey: "metadata.field.editor.v1", CapabilityKeys: []string{"schema.field"},
			Route: "/system/metadata", RequiredPermissions: []string{"workspace.admin"},
			FeatureModule: "features/system/metadata.tsx", AcceptanceTests: []string{"tests/e2e/metadata.spec.ts"},
		}},
	}
	registered, err := service.RegisterManifest(t.Context(), manifest, frontendCapabilityAdmin("workspace-a"))
	if err != nil {
		t.Fatal(err)
	}
	if registered.RuntimeContractVersion != "runtime-authoring-v1" || !registered.ContractCompatible ||
		registered.SchemaHash != "schema-hash" || registered.SchemaSnapshotVersion != "schema-snapshot-7" ||
		registered.RuntimeCapabilityCount != 1 || registered.FrontendSupportCount != 1 {
		t.Fatalf("registered diagnostics=%#v", registered)
	}
	unknown, err := service.Snapshot(t.Context(), frontendCapabilityAdmin("workspace-b"))
	if err != nil || unknown.Status != "unknown" || unknown.RuntimeContractVersion != "runtime-authoring-v1" ||
		unknown.SchemaHash != "schema-hash" || unknown.RuntimeCapabilityCount != 1 || unknown.FrontendSupportCount != 0 {
		t.Fatalf("unknown diagnostics=%#v err=%v", unknown, err)
	}
}
