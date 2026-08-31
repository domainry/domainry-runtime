package provision

import (
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"

	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestInitialManifestProvisionIsUnauthenticatedAndIdempotent(t *testing.T) {
	manifest := provisionTestManifest(t)
	target := filepath.Join(t.TempDir(), "instance", "domainry.template.json")
	activations := 0
	server := NewServer(target, "1.2.3", testContractIdentity(), func(got manifestmodel.ManifestSchema) error {
		activations++
		if got.TemplateID != manifest.TemplateID {
			t.Fatalf("activated template %q, want %q", got.TemplateID, manifest.TemplateID)
		}
		return nil
	}).Routes()

	review := provisionRequest(t, server, http.MethodPost, "/metadata/manifests/review", map[string]any{"manifest": manifest})
	if review.Code != http.StatusOK {
		t.Fatalf("review status %d: %s", review.Code, review.Body.String())
	}
	var reviewed map[string]any
	decodeProvisionResponse(t, review, &reviewed)
	if reviewed["initial_install"] != true || reviewed["current_snapshot_hash"] != emptySnapshotHash {
		t.Fatalf("unexpected initial review: %#v", reviewed)
	}
	hash, _ := reviewed["manifest_hash"].(string)
	applyPayload := map[string]any{
		"manifest": manifest, "reviewed_manifest_hash": hash, "expected_snapshot_hash": emptySnapshotHash,
		"actor": "builder-agent", "reason": "initial build",
	}
	applied := provisionRequest(t, server, http.MethodPost, "/metadata/manifests/apply", applyPayload)
	if applied.Code != http.StatusCreated {
		t.Fatalf("apply status %d: %s", applied.Code, applied.Body.String())
	}
	if activations != 1 {
		t.Fatalf("activations = %d, want 1", activations)
	}
	var appliedPayload map[string]any
	decodeProvisionResponse(t, applied, &appliedPayload)
	if _, leaked := appliedPayload["initial_credentials"]; leaked {
		t.Fatalf("Runtime Provision leaked Identity credentials: %#v", appliedPayload)
	}
	if _, leaked := appliedPayload["credential_delivery"]; leaked {
		t.Fatalf("Runtime Provision retained Identity credential delivery metadata: %#v", appliedPayload)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("provisioned manifest missing: %v", err)
	}
	current := provisionRequest(t, server, http.MethodGet, "/metadata/manifests/current", nil)
	if current.Code != http.StatusOK {
		t.Fatalf("current status %d: %s", current.Code, current.Body.String())
	}
	var currentPayload struct {
		ManifestHash    string `json:"manifest_hash"`
		RuntimeMetadata struct {
			ModelVersion string                       `json:"model_version"`
			TemplateID   string                       `json:"template_id"`
			ManifestHash string                       `json:"manifest_hash"`
			Manifest     manifestmodel.ManifestSchema `json:"manifest"`
		} `json:"runtime_metadata"`
	}
	decodeProvisionResponse(t, current, &currentPayload)
	if currentPayload.RuntimeMetadata.ModelVersion != "runtime-native-metadata-v1" || currentPayload.RuntimeMetadata.TemplateID != manifest.TemplateID || currentPayload.RuntimeMetadata.ManifestHash != currentPayload.ManifestHash || currentPayload.RuntimeMetadata.Manifest.TemplateID != manifest.TemplateID {
		t.Fatalf("unaligned current Runtime metadata: %#v", currentPayload)
	}
	auditPath := filepath.Join(filepath.Dir(target), "provision-audit.jsonl")
	auditPayload, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("provision audit missing: %v", err)
	}
	if !bytes.Contains(auditPayload, []byte(`"result":"applied"`)) || !bytes.Contains(auditPayload, []byte(`"actor":"builder-agent"`)) {
		t.Fatalf("provision audit lacks evidence: %s", auditPayload)
	}

	applyPayload["expected_snapshot_hash"] = hash
	replayed := provisionRequest(t, server, http.MethodPost, "/metadata/manifests/apply", applyPayload)
	if replayed.Code != http.StatusOK {
		t.Fatalf("replay status %d: %s", replayed.Code, replayed.Body.String())
	}
	var replay map[string]any
	decodeProvisionResponse(t, replayed, &replay)
	if replay["status"] != "noop" || activations != 1 {
		t.Fatalf("unexpected replay: %#v; activations=%d", replay, activations)
	}
	if _, leaked := replay["initial_credentials"]; leaked {
		t.Fatalf("idempotent replay leaked Identity credentials: %#v", replay)
	}
	auditPayload, err = os.ReadFile(auditPath)
	if err != nil || !bytes.Contains(auditPayload, []byte(`"result":"noop"`)) {
		t.Fatalf("idempotent audit missing: %v: %s", err, auditPayload)
	}
}

func TestManifestProvisionRejectsStaleSnapshot(t *testing.T) {
	manifest := provisionTestManifest(t)
	server := NewServer(filepath.Join(t.TempDir(), "domainry.template.json"), "dev", testContractIdentity(), nil).Routes()
	hash, err := validateAndHash(manifest, testContractIdentity())
	if err != nil {
		t.Fatal(err)
	}
	response := provisionRequest(t, server, http.MethodPost, "/metadata/manifests/apply", map[string]any{
		"manifest": manifest, "reviewed_manifest_hash": hash, "expected_snapshot_hash": "stale",
		"actor": "builder-agent", "reason": "initial build",
	})
	if response.Code != http.StatusConflict || !bytes.Contains(response.Body.Bytes(), []byte(`"code":"stale_snapshot"`)) {
		t.Fatalf("unexpected stale response %d: %s", response.Code, response.Body.String())
	}
}

func TestProvisionBootstrapRoutesExposeContractAndValidation(t *testing.T) {
	manifest := provisionTestManifest(t)
	server := NewServer(filepath.Join(t.TempDir(), "domainry.template.json"), "1.2.3", testContractIdentity(), nil).UseProductBrand("Acme").Routes()

	identity := provisionRequest(t, server, http.MethodGet, "/", nil)
	if identity.Code != http.StatusOK {
		t.Fatalf("identity status=%d body=%s", identity.Code, identity.Body.String())
	}
	var identityPayload map[string]any
	decodeProvisionResponse(t, identity, &identityPayload)
	if identityPayload["status"] != "provisioning" || identityPayload["service_kind"] != "domain-runtime" || identityPayload["api_contract_version"] != "runtime-domain-api-v1" || identityPayload["api_contract_hash"] != testContractIdentity().APIContractHash {
		t.Fatalf("identity payload=%#v", identityPayload)
	}

	health := provisionRequest(t, server, http.MethodGet, "/health", nil)
	if health.Code != http.StatusOK {
		t.Fatalf("health status=%d body=%s", health.Code, health.Body.String())
	}
	var healthPayload map[string]any
	decodeProvisionResponse(t, health, &healthPayload)
	if healthPayload["status"] != "provisioning" || healthPayload["ready"] != false {
		t.Fatalf("health payload=%#v", healthPayload)
	}

	openapi := provisionRequest(t, server, http.MethodGet, "/openapi.json", nil)
	if openapi.Code != http.StatusOK {
		t.Fatalf("openapi status=%d body=%s", openapi.Code, openapi.Body.String())
	}
	var openapiPayload struct {
		OpenAPI string `json:"openapi"`
		Info    struct {
			Title   string `json:"title"`
			Version string `json:"version"`
		} `json:"info"`
		Paths map[string]any `json:"paths"`
	}
	decodeProvisionResponse(t, openapi, &openapiPayload)
	if openapiPayload.OpenAPI != "3.1.0" || openapiPayload.Info.Title != "Acme domain Runtime Provision API" || openapiPayload.Info.Version != "1.2.3" || len(openapiPayload.Paths) != 10 {
		t.Fatalf("openapi payload=%#v", openapiPayload)
	}
	for _, path := range []string{"/metadata/manifests/validate", "/metadata/manifests/current", "/provision/review", "/provision/apply", "/provision/rollback", "/provision/configuring", "/provision/lifecycle", "/provision/abandon"} {
		if _, ok := openapiPayload.Paths[path]; !ok {
			t.Fatalf("openapi path %q missing: %#v", path, openapiPayload.Paths)
		}
	}

	validated := provisionRequest(t, server, http.MethodPost, "/metadata/manifests/validate", map[string]any{"manifest": manifest})
	if validated.Code != http.StatusOK {
		t.Fatalf("validate status=%d body=%s", validated.Code, validated.Body.String())
	}
	var validatedPayload map[string]any
	decodeProvisionResponse(t, validated, &validatedPayload)
	if validatedPayload["valid"] != true || validatedPayload["template_id"] != manifest.TemplateID || validatedPayload["manifest_hash"] != manifest.ManifestHash {
		t.Fatalf("validate payload=%#v", validatedPayload)
	}

	malformed := httptest.NewRecorder()
	server.ServeHTTP(malformed, httptest.NewRequest(http.MethodPost, "/metadata/manifests/validate", bytes.NewBufferString(`{"manifest":`)))
	if malformed.Code != http.StatusBadRequest || !bytes.Contains(malformed.Body.Bytes(), []byte(`"code":"invalid_json"`)) {
		t.Fatalf("malformed status=%d body=%s", malformed.Code, malformed.Body.String())
	}
	invalid := provisionRequest(t, server, http.MethodPost, "/metadata/manifests/validate", map[string]any{"manifest": map[string]any{}})
	if invalid.Code != http.StatusUnprocessableEntity || !bytes.Contains(invalid.Body.Bytes(), []byte(`"code":"manifest_validation_failed"`)) {
		t.Fatalf("invalid status=%d body=%s", invalid.Code, invalid.Body.String())
	}
}

func TestInitialProvisionCarriesIdentityProfileExtensionIntoActivation(t *testing.T) {
	manifest := provisionTestManifest(t)
	manifest.Objects = append(manifest.Objects, definitionmodel.ObjectSchema{
		Key: "employee_profile", Name: "Employee Profile",
		Fields: []definitionmodel.FieldSchema{
			{Key: "identity_user", Name: "Identity User", Type: "relation", Required: true, Unique: true, Config: map[string]any{"object_key": "identity_user"}},
			{Key: "legal_entity", Name: "Legal Entity", Type: "text"},
		},
		UX: map[string]any{"kind": "identity_profile_extension", "config": map[string]any{"identity_relation_field": "identity_user"}},
	})
	manifest.IdentityProfileExtensions = []profilebindingmodel.Binding{{
		ContractVersion: profilebindingmodel.ContractVersion, MinReaderVersion: profilebindingmodel.MinimumReaderVersion,
		ObjectKey: "employee_profile", IdentityRelationField: "identity_user", Cardinality: "one_to_one",
		BusinessIdentity: profilebindingmodel.BusinessIdentityBinding{Key: "employee"},
		SummaryFields:    []string{"legal_entity"}, ProfileTabs: []string{"employment"},
		ProfileTabLabels: map[string]string{"employment": "Employment"}, ProfileTabFields: map[string][]string{"employment": {"legal_entity"}},
		ProfileTabRelatedObjects: map[string][]string{}, ProfileTabComponents: map[string][]string{},
		DefaultVisibility: "when_readable", RequiredPermissions: []string{"employee_profile.read"},
	}}
	var err error
	manifest.ManifestHash, err = manifestHash(manifest)
	if err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(t.TempDir(), "domainry.template.json")
	activated := false
	server := NewServer(target, "dev", testContractIdentity(), func(got manifestmodel.ManifestSchema) error {
		activated = true
		if len(got.IdentityProfileExtensions) != 1 || got.IdentityProfileExtensions[0].ObjectKey != "employee_profile" {
			t.Fatalf("Provision activation lost Profile Extension: %#v", got.IdentityProfileExtensions)
		}
		return nil
	}).Routes()
	review := provisionRequest(t, server, http.MethodPost, "/metadata/manifests/review", map[string]any{"manifest": manifest})
	if review.Code != http.StatusOK {
		t.Fatalf("Profile Provision review status=%d body=%s", review.Code, review.Body.String())
	}
	var reviewed struct {
		ManifestHash        string `json:"manifest_hash"`
		CurrentSnapshotHash string `json:"current_snapshot_hash"`
	}
	decodeProvisionResponse(t, review, &reviewed)
	apply := provisionRequest(t, server, http.MethodPost, "/metadata/manifests/apply", map[string]any{
		"manifest": manifest, "reviewed_manifest_hash": reviewed.ManifestHash, "expected_snapshot_hash": reviewed.CurrentSnapshotHash,
		"actor": "builder-agent", "reason": "install Profile Extension",
	})
	if apply.Code != http.StatusCreated || !activated {
		t.Fatalf("Profile Provision apply status=%d activated=%v body=%s", apply.Code, activated, apply.Body.String())
	}
}

func TestBuilderV1ProvisionReviewApplyRollbackReturnsReceiptIdentities(t *testing.T) {
	manifest := provisionTestManifest(t)
	target := filepath.Join(t.TempDir(), "instance", "domainry.template.json")
	activations := 0
	server := NewServer(target, "1.2.3", testContractIdentity(), func(got manifestmodel.ManifestSchema) error {
		activations++
		if got.TemplateID != manifest.TemplateID {
			t.Fatalf("activated template %q, want %q", got.TemplateID, manifest.TemplateID)
		}
		return nil
	}).Routes()
	request := builderV1ProvisionRequest(t, manifest, "1.2.3")

	review := provisionRequest(t, server, http.MethodPost, "/provision/review", map[string]any{"provision_request": request})
	if review.Code != http.StatusOK {
		t.Fatalf("v1 review status %d: %s", review.Code, review.Body.String())
	}
	var reviewed struct {
		Status               string `json:"status"`
		RequestHash          string `json:"request_hash"`
		ReviewedManifestHash string `json:"reviewed_manifest_hash"`
		DiagnosticCount      int    `json:"diagnostic_count"`
	}
	decodeProvisionResponse(t, review, &reviewed)
	if reviewed.Status != "ready" || reviewed.RequestHash != request["request_hash"] || reviewed.ReviewedManifestHash != manifest.ManifestHash || reviewed.DiagnosticCount != 0 {
		t.Fatalf("unexpected v1 review: %#v", reviewed)
	}

	apply := provisionRequest(t, server, http.MethodPost, "/provision/apply", map[string]any{"provision_request": request})
	if apply.Code != http.StatusCreated {
		t.Fatalf("v1 apply status %d: %s", apply.Code, apply.Body.String())
	}
	var applied struct {
		Status  string         `json:"status"`
		Receipt map[string]any `json:"receipt"`
	}
	decodeProvisionResponse(t, apply, &applied)
	if applied.Status != "applied" || activations != 1 {
		t.Fatalf("unexpected v1 apply: %#v activations=%d", applied, activations)
	}
	for _, key := range []string{"request_hash", "foundation_hash", "blueprint_hash", "runtime_manifest_hash", "authoring_contract_hash", "target_api_contract_hash", "application_lock_hash", "release_manifest_hash", "runtime_package_hash", "runtime_instance_config_hash"} {
		if applied.Receipt[key] != request[key] {
			t.Fatalf("receipt %s=%#v, want request value %#v", key, applied.Receipt[key], request[key])
		}
	}
	if applied.Receipt["runtime_api_contract_hash"] != testContractIdentity().APIContractHash || applied.Receipt["installed_manifest_hash"] != manifest.ManifestHash || applied.Receipt["runtime_version"] != "1.2.3" {
		t.Fatalf("receipt lacks Runtime identities: %#v", applied.Receipt)
	}
	for _, key := range []string{"seed_graph_hash", "effective_permission_hash"} {
		if value, _ := applied.Receipt[key].(string); value == "" {
			t.Fatalf("receipt missing %s: %#v", key, applied.Receipt)
		}
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("manifest not installed: %v", err)
	}

	rollback := provisionRequest(t, server, http.MethodPost, "/provision/rollback", map[string]any{"request_hash": request["request_hash"], "receipt_hash": "receipt-hash", "reason": "test rollback"})
	if rollback.Code != http.StatusOK {
		t.Fatalf("v1 rollback status %d: %s", rollback.Code, rollback.Body.String())
	}
	var rolledBack struct {
		Status      string `json:"status"`
		RequestHash string `json:"request_hash"`
	}
	decodeProvisionResponse(t, rollback, &rolledBack)
	if rolledBack.Status != "rolled_back" || rolledBack.RequestHash != request["request_hash"] {
		t.Fatalf("unexpected rollback: %#v", rolledBack)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("manifest still exists after rollback: %v", err)
	}
}

func TestBuilderV1ProvisionReviewCollectsRequestDiagnostics(t *testing.T) {
	server := NewServer(filepath.Join(t.TempDir(), "domainry.template.json"), "dev", testContractIdentity(), nil).Routes()
	review := provisionRequest(t, server, http.MethodPost, "/provision/review", map[string]any{"provision_request": map[string]any{"foundation_hash": "foundation"}})
	if review.Code != http.StatusOK {
		t.Fatalf("v1 review status %d: %s", review.Code, review.Body.String())
	}
	var payload struct {
		Status      string `json:"status"`
		Diagnostics []struct {
			Code string `json:"code"`
		} `json:"diagnostics"`
	}
	decodeProvisionResponse(t, review, &payload)
	codes := map[string]bool{}
	for _, diagnostic := range payload.Diagnostics {
		codes[diagnostic.Code] = true
	}
	if payload.Status != "blocked" || !codes["runtime.manifest_missing"] || !codes["runtime.request_hash_missing"] {
		t.Fatalf("expected collect-all diagnostics, got %#v", payload)
	}
}

func testContractIdentity() ContractIdentity {
	return ContractIdentity{ServiceKind: "domain-runtime", APIContractVersion: "runtime-domain-api-v1", APIContractHash: "c3a655f6879459a5a7aa447e56f65a49c698424140a279d6bcf57ba4aaca0f70"}
}

func provisionTestManifest(t *testing.T) manifestmodel.ManifestSchema {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join("..", "..", "domain", "manifest", "testdata", "manifests", "domain-only-minimal.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest manifestmodel.ManifestSchema
	if err := json.Unmarshal(payload, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.TargetAPIContractVersion = "runtime-domain-api-v1"
	manifest.TargetAPIContractHash = "c3a655f6879459a5a7aa447e56f65a49c698424140a279d6bcf57ba4aaca0f70"
	manifest.ManifestHash, err = manifestHash(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

func builderV1ProvisionRequest(t *testing.T, manifest manifestmodel.ManifestSchema, runtimeVersion string) map[string]any {
	t.Helper()
	request := map[string]any{
		"foundation_hash":              "foundation",
		"blueprint_hash":               "blueprint",
		"blueprint_review_hash":        "review",
		"runtime_manifest_hash":        manifest.ManifestHash,
		"authoring_contract_hash":      manifest.AuthoringContractHash,
		"target_api_contract_hash":     manifest.TargetAPIContractHash,
		"application_lock_hash":        "application-lock",
		"release_manifest_hash":        "release",
		"runtime_version":              runtimeVersion,
		"runtime_package_hash":         "runtime-package",
		"runtime_instance_config_hash": "runtime-instance",
		"route_registry":               map[string]any{"routes": []any{}},
		"manifest":                     manifest,
	}
	request["request_hash"] = hashProvisionTestValue(t, request)
	return request
}

func hashProvisionTestValue(t *testing.T, value any) string {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func provisionRequest(t *testing.T, handler http.Handler, method, path string, payload any) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	if payload != nil {
		if err := json.NewEncoder(&body).Encode(payload); err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest(method, path, &body)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func decodeProvisionResponse(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), target); err != nil {
		t.Fatalf("decode response: %v: %s", err, response.Body.String())
	}
}
