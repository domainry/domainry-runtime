package provision

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func TestDecodeV1ProvisionRequestDiagnosticsAndManifestProjection(t *testing.T) {
	server := NewServer(filepath.Join(t.TempDir(), "manifest.json"), "dev", testContractIdentity(), nil)
	tests := []struct {
		name      string
		body      string
		ok        bool
		codes     []string
		wantKey   string
		wantValue string
	}{
		{name: "invalid json", body: `{`, ok: false},
		{name: "unknown envelope field", body: `{"other":true}`, ok: false},
		{name: "missing request", body: `{}`, ok: true, codes: []string{"runtime.provision_request_missing"}},
		{name: "missing manifest and hash", body: `{"provision_request":{"foundation_hash":"foundation"}}`, ok: true, codes: []string{"runtime.manifest_missing", "runtime.request_hash_missing"}, wantKey: "foundation_hash", wantValue: "foundation"},
		{name: "invalid manifest", body: `{"provision_request":{"manifest":"invalid","request_hash":"request"}}`, ok: true, codes: []string{"runtime.manifest_invalid"}},
		{name: "manifest without hash", body: `{"provision_request":{"manifest":{}}}`, ok: true, codes: []string{"runtime.request_hash_missing"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/provision/review", strings.NewReader(test.body))
			request, _, diagnostics, ok := server.decodeV1ProvisionRequest(w, r)
			if ok != test.ok {
				t.Fatalf("ok=%v status=%d body=%s", ok, w.Code, w.Body.String())
			}
			if !test.ok {
				if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `"code":"invalid_json"`) {
					t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
				}
				return
			}
			seen := map[string]bool{}
			for _, diagnostic := range diagnostics {
				seen[diagnostic.Code] = true
				if diagnostic.Repair["operation"] == "" {
					t.Fatalf("diagnostic lacks repair=%#v", diagnostic)
				}
			}
			for _, code := range test.codes {
				if !seen[code] {
					t.Fatalf("missing code=%s diagnostics=%#v", code, diagnostics)
				}
			}
			if test.wantKey != "" && stringFromMap(request, test.wantKey) != test.wantValue {
				t.Fatalf("request=%#v", request)
			}
		})
	}
}

func TestLoadCurrentCoversMissingMalformedDirectoryAndValidManifest(t *testing.T) {
	target := filepath.Join(t.TempDir(), "instance", "manifest.json")
	server := NewServer(target, "dev", testContractIdentity(), nil)
	if _, hash, found, err := server.loadCurrent(); err != nil || found || hash != "" {
		t.Fatalf("missing found=%v hash=%q err=%v", found, hash, err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(`{`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := server.loadCurrent(); err == nil {
		t.Fatal("malformed current manifest accepted")
	}
	manifest := provisionTestManifest(t)
	payload, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	got, hash, found, err := server.loadCurrent()
	if err != nil || !found || got.TemplateID != manifest.TemplateID || hash != manifest.ManifestHash {
		t.Fatalf("current=%#v found=%v hash=%q err=%v", got, found, hash, err)
	}
	directoryServer := NewServer(filepath.Dir(target), "dev", testContractIdentity(), nil)
	if _, _, _, err := directoryServer.loadCurrent(); err == nil {
		t.Fatal("directory manifest path accepted")
	}
}

func TestValidateAndHashRejectsMissingMismatchAndContractDrift(t *testing.T) {
	if _, err := validateAndHash(manifestmodel.ManifestSchema{}, testContractIdentity()); err == nil {
		t.Fatal("empty manifest accepted")
	}
	manifest := provisionTestManifest(t)
	manifest.ManifestHash = ""
	if _, err := validateAndHash(manifest, testContractIdentity()); err == nil || !strings.Contains(err.Error(), "manifest_hash is required") {
		t.Fatalf("missing hash err=%v", err)
	}
	manifest = provisionTestManifest(t)
	manifest.ManifestHash = strings.Repeat("0", 64)
	if _, err := validateAndHash(manifest, testContractIdentity()); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("mismatch err=%v", err)
	}
	manifest = provisionTestManifest(t)
	contract := testContractIdentity()
	contract.APIContractHash = "other"
	if _, err := validateAndHash(manifest, contract); err == nil || !strings.Contains(err.Error(), "incompatible") {
		t.Fatalf("contract err=%v", err)
	}
}

func TestWriteV1ApplyBlockedAndSupportValueHelpers(t *testing.T) {
	server := NewServer(filepath.Join(t.TempDir(), "manifest.json"), "1.2.3", testContractIdentity(), nil)
	w := httptest.NewRecorder()
	server.writeV1ApplyBlocked(w, map[string]any{"request_hash": " request "}, "runtime.blocked", "/manifest", "blocked", "repair")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"status":"blocked"`) || !strings.Contains(w.Body.String(), `"request_hash":"request"`) || !strings.Contains(w.Body.String(), `"operation":"repair"`) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if stringFromMap(map[string]any{"nil": nil}, "nil") != "" || stringFromMap(map[string]any{}, "missing") != "" || stringFromMap(map[string]any{"number": 7}, "number") != "7" {
		t.Fatal("string map projection mismatch")
	}
	if valueOrDefault(" value ", "fallback") != " value " || valueOrDefault(" ", "fallback") != "fallback" {
		t.Fatal("value fallback mismatch")
	}
	if snapshotHash(false, "ignored") != emptySnapshotHash || snapshotHash(true, "hash") != "hash" {
		t.Fatal("snapshot hash mismatch")
	}
	if hashManifestPart(map[string]any{"value": 1}) == "" || manifestHashMustMatch(t, provisionTestManifest(t)) == "" {
		t.Fatal("hash helper returned empty")
	}
}

func manifestHashMustMatch(t *testing.T, manifest manifestmodel.ManifestSchema) string {
	t.Helper()
	hash, err := manifestHash(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return hash
}

func TestWriteManifestAtomicSuccessAndFilesystemFailures(t *testing.T) {
	manifest := provisionTestManifest(t)
	target := filepath.Join(t.TempDir(), "nested", "manifest.json")
	if err := writeManifestAtomic(target, manifest); err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(target)
	if err != nil || !bytes.HasSuffix(payload, []byte("\n")) {
		t.Fatalf("payload err=%v suffix=%q", err, payload[len(payload)-1:])
	}
	var decoded manifestmodel.ManifestSchema
	if err := json.Unmarshal(payload, &decoded); err != nil || decoded.TemplateID != manifest.TemplateID {
		t.Fatalf("decoded=%#v err=%v", decoded, err)
	}
	parentFile := filepath.Join(t.TempDir(), "parent")
	if err := os.WriteFile(parentFile, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeManifestAtomic(filepath.Join(parentFile, "manifest.json"), manifest); err == nil {
		t.Fatal("file parent accepted")
	}
	directoryTarget := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.MkdirAll(directoryTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeManifestAtomic(directoryTarget, manifest); err == nil || !strings.Contains(err.Error(), "install Runtime manifest") {
		t.Fatalf("directory target err=%v", err)
	}
}

func TestAppendAuditWritesEvidenceAndRejectsInvalidParent(t *testing.T) {
	target := filepath.Join(t.TempDir(), "instance", "manifest.json")
	server := NewServer(target, "1.2.3", testContractIdentity(), nil)
	manifest := provisionTestManifest(t)
	request := manifestRequest{Manifest: manifest, Actor: " builder ", Reason: " test ", ExpectedSnapshotHash: emptySnapshotHash}
	if err := server.appendAudit(request, manifest.ManifestHash, "applied", "boundary"); err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(server.auditPath())
	if err != nil || !bytes.Contains(payload, []byte(`"actor":"builder"`)) || !bytes.Contains(payload, []byte(`"result":"applied"`)) {
		t.Fatalf("audit=%s err=%v", payload, err)
	}
	parentFile := filepath.Join(t.TempDir(), "parent")
	if err := os.WriteFile(parentFile, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	broken := NewServer(filepath.Join(parentFile, "manifest.json"), "dev", testContractIdentity(), nil)
	if err := broken.appendAudit(request, manifest.ManifestHash, "failed", "boundary"); err == nil {
		t.Fatal("invalid audit parent accepted")
	}
}
