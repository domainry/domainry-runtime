package deploymentseed

import (
	"context"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"

	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"

	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/domainry/domainry-foundation/requestcontext"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	runtimetestkit "github.com/domainry/domainry-runtime/runtime/bootstrap/testkit"
)

type frontendCapabilityRegistrarProbe struct {
	err              error
	principal        principalmodel.Principal
	contextWorkspace string
}

func (p *frontendCapabilityRegistrarProbe) RegisterManifest(ctx context.Context, _ deploymentmodel.FrontendCapabilityManifest, principal principalmodel.Principal) (deploymentmodel.FrontendCapabilitySnapshot, error) {
	p.principal = principal
	p.contextWorkspace = requestcontext.WorkspaceID(ctx)
	return deploymentmodel.FrontendCapabilitySnapshot{}, p.err
}

func TestInstallFrontendCapabilityManifestSeedsSeparateDeploymentEvidence(t *testing.T) {
	records := runtimetestkit.NewRuntimeServices(t.Context(), runtimetestkit.RuntimeServicesConfig{TemplateID: "crm", TemplateVersion: "1", Name: "CRM", Objects: []definitionmodel.ObjectSchema{{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}}, Views: nil, Actions: nil, Workflows: nil, AutomationRules: nil, Dictionaries: nil, Integrations: integrationmodel.IntegrationSchema{}, Reports: nil, Entrypoints: nil, Skills: nil, Agents: nil, Store: nil})
	manifest := deploymentmodel.FrontendCapabilityManifest{
		ManifestVersion: deploymentmodel.FrontendCapabilityManifestVersion, FrontendVersion: "frontend-1",
		RuntimeContractVersions: []string{capabilitycontract.RuntimeAuthoringContractVersion},
		DeploymentEvidence: &deploymentmodel.FrontendDeploymentEvidence{
			AuditContractVersion: "domainry-frontend-verification-evidence-v1",
			DesignContractHash:   repeatedFrontendSeedHash("a"), RouteRegistryHash: repeatedFrontendSeedHash("b"),
			FrontendSourceHash: repeatedFrontendSeedHash("c"), AuditArtifactHash: repeatedFrontendSeedHash("d"),
		},
		Entries: []deploymentmodel.FrontendCapabilitySupportEntry{{SupportKey: "domain.route.customers.v1", Route: "/customers", FeatureModule: "features/customers", AcceptanceTests: []string{"tests/customers.spec.ts"}, ActorRoles: []string{"sales"}, BusinessObjects: []string{"customer"}, FieldKeys: []string{"customer.name"}}},
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "frontend-capability-manifest.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := InstallFrontendCapabilityManifest(t.Context(), records.Applications().FrontendCapabilities, path); err != nil {
		t.Fatal(err)
	}
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "__default__"}}
	snapshot, err := records.Applications().FrontendCapabilities.Snapshot(t.Context(), principal)
	if err != nil || snapshot.Manifest == nil || snapshot.Manifest.FrontendVersion != "frontend-1" {
		t.Fatalf("frontend deployment evidence was not installed: snapshot=%#v err=%v", snapshot, err)
	}
}

func repeatedFrontendSeedHash(value string) string {
	result := ""
	for len(result) < 64 {
		result += value
	}
	return result
}

func TestInstallFrontendCapabilityManifestFailureBoundaries(t *testing.T) {
	probe := &frontendCapabilityRegistrarProbe{}
	if err := InstallFrontendCapabilityManifest(t.Context(), probe, "  "); err != nil {
		t.Fatalf("empty path error = %v", err)
	}
	missing := filepath.Join(t.TempDir(), "missing.json")
	if err := InstallFrontendCapabilityManifest(t.Context(), probe, missing); err == nil || !strings.Contains(err.Error(), "read frontend capability manifest") {
		t.Fatalf("missing path error = %v", err)
	}
	invalid := filepath.Join(t.TempDir(), "invalid.json")
	if err := os.WriteFile(invalid, []byte(`{"unknown":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := InstallFrontendCapabilityManifest(t.Context(), probe, invalid); err == nil || !strings.Contains(err.Error(), "decode frontend capability manifest") {
		t.Fatalf("decode error = %v", err)
	}
	valid := filepath.Join(t.TempDir(), "valid.json")
	if err := os.WriteFile(valid, []byte(`{"manifest_version":"frontend-capability.v1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	registrationFailure := errors.New("registration failed")
	probe.err = registrationFailure
	if err := InstallFrontendCapabilityManifest(t.Context(), probe, valid); !errors.Is(err, registrationFailure) || !strings.Contains(err.Error(), "install frontend capability manifest") {
		t.Fatalf("registration error = %v", err)
	}
	if !probe.principal.Known || probe.principal.WorkspaceID != "__default__" || !probe.principal.SystemScope.Valid() {
		t.Fatalf("principal=%#v", probe.principal)
	}
	if probe.contextWorkspace != "__default__" {
		t.Fatalf("context workspace=%q", probe.contextWorkspace)
	}
}
