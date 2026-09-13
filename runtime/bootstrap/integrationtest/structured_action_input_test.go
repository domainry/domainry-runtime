package integrationtest

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	integrationmodule "github.com/domainry/domainry-integration/module"
	notificationmodule "github.com/domainry/domainry-notification/module"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	bootstrap "github.com/domainry/domainry-runtime/runtime/bootstrap"
	runtimetestkit "github.com/domainry/domainry-runtime/runtime/bootstrap/testkit"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	dataexchangefixture "github.com/domainry/domainry-runtime/testsupport/dataexchangefixture"
	schedulermodule "github.com/domainry/domainry-scheduler/module"
)

const (
	structuredActionRole    = "structured_admin"
	structuredActionUser    = "structured_user"
	structuredRegisterKey   = "customer.register_accounts"
	structuredLegacyKey     = "customer.legacy_rename"
	structuredInputTypeBase = "example.com/domainry/integrationtest/structured."
)

// structuredRegisterAccountsInput is the typed Go shape a generated Business
// Handler decodes from the governed Runtime payload.
type structuredRegisterAccountsInput struct {
	Name     string `json:"name"`
	Accounts []struct {
		Bank    string `json:"bank"`
		Number  string `json:"number"`
		Primary bool   `json:"primary"`
	} `json:"accounts"`
	Steps []struct {
		Title             string   `json:"title"`
		RequiredApprovals int      `json:"required_approvals"`
		Assignees         []string `json:"assignees"`
	} `json:"steps"`
}

type structuredActionHandler struct {
	descriptor runtimeext.HandlerDescriptor
	mu         sync.Mutex
	rawInputs  []string
	decoded    []structuredRegisterAccountsInput
}

func (h *structuredActionHandler) Descriptor() runtimeext.HandlerDescriptor { return h.descriptor }

func (h *structuredActionHandler) Invoke(ctx context.Context, execution runtimeext.ActionExecution, raw json.RawMessage) (json.RawMessage, error) {
	switch execution.Identity().ActionKey {
	case structuredRegisterKey:
		var input structuredRegisterAccountsInput
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			return nil, &runtimeext.BusinessError{Code: "structured.decode_failed", Message: err.Error()}
		}
		h.mu.Lock()
		h.rawInputs = append(h.rawInputs, string(raw))
		h.decoded = append(h.decoded, input)
		h.mu.Unlock()
		parent, err := execution.ApplyRecordMutation(ctx, runtimeext.RecordMutation{
			Operation: runtimeext.MutationCreate, ObjectKey: "customer", Fields: map[string]any{"name": input.Name},
		})
		if err != nil {
			return nil, err
		}
		childIDs := []string{}
		for _, account := range input.Accounts {
			child, err := execution.ApplyRecordMutation(ctx, runtimeext.RecordMutation{
				Operation: runtimeext.MutationCreate, ObjectKey: "customer_account",
				Fields: map[string]any{"customer": parent.Record.ID, "bank": account.Bank, "number": account.Number, "primary": account.Primary},
			})
			if err != nil {
				return nil, err
			}
			childIDs = append(childIDs, child.Record.ID)
		}
		return json.Marshal(map[string]any{"customer_id": parent.Record.ID, "account_ids": childIDs, "step_count": len(input.Steps)})
	case structuredLegacyKey:
		var input struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, err
		}
		identity := execution.Identity()
		if _, err := execution.ApplyRecordMutation(ctx, runtimeext.RecordMutation{
			Operation: runtimeext.MutationUpdate, ObjectKey: identity.ObjectKey, RecordID: identity.RecordID, Fields: map[string]any{"name": input.Name},
		}); err != nil {
			return nil, err
		}
		return json.RawMessage(`{}`), nil
	default:
		return nil, &runtimeext.BusinessError{Code: "structured.unknown_action"}
	}
}

func structuredActionManifest(t *testing.T, directory string) string {
	t.Helper()
	contract := func(actionKey, suffix string) map[string]any {
		return map[string]any{
			"input_type": structuredInputTypeBase + suffix + "Input", "output_type": structuredInputTypeBase + suffix + "Output",
			"input_contract_sha256": sourceOwnedFixtureContractHash(actionKey + ":input"), "output_contract_sha256": sourceOwnedFixtureContractHash(actionKey + ":output"),
		}
	}
	register := map[string]any{
		"key": structuredRegisterKey, "object_key": "customer", "label": "Register accounts", "kind": "object_operation", "preconditions": []any{}, "audit_event": "customer_accounts_registered",
		"payload_fields": []any{
			map[string]any{"key": "name", "name": "Name", "type": "text", "required": true},
			map[string]any{"key": "accounts", "name": "Accounts", "type": "object", "repeated": true, "required": true, "max_items": 3, "fields": []any{
				map[string]any{"key": "bank", "type": "text", "required": true},
				map[string]any{"key": "number", "type": "text", "required": true},
				map[string]any{"key": "primary", "type": "boolean"},
			}},
			map[string]any{"key": "steps", "name": "Steps", "type": "object", "repeated": true, "fields": []any{
				map[string]any{"key": "title", "type": "text", "required": true},
				map[string]any{"key": "required_approvals", "type": "integer"},
				map[string]any{"key": "assignees", "type": "user", "repeated": true, "required": true},
			}},
		},
	}
	for key, value := range contract(structuredRegisterKey, "RegisterAccounts") {
		register[key] = value
	}
	legacy := map[string]any{
		"key": structuredLegacyKey, "object_key": "customer", "label": "Legacy rename", "kind": "record_operation", "preconditions": []any{}, "audit_event": "customer_renamed",
		"payload_fields": []any{map[string]any{"key": "name", "name": "Name", "type": "text", "required": true}},
	}
	for key, value := range contract(structuredLegacyKey, "LegacyRename") {
		legacy[key] = value
	}
	permissions := []any{}
	for _, permission := range structuredActionPermissions() {
		permissions = append(permissions, map[string]any{"permission_key": permission, "data_scope": "all"})
	}
	manifest := map[string]any{
		"template_id": "structured_action_input", "version": "0.1.0", "name": "Structured Action Input", "schema_version": "2",
		"objects": []any{
			map[string]any{"key": "customer", "name": "Customer", "fields": []any{
				map[string]any{"key": "name", "name": "Name", "type": "text", "required": true},
			}},
			map[string]any{"key": "customer_account", "name": "Customer account", "fields": []any{
				map[string]any{"key": "customer", "name": "Customer", "type": "relation", "required": true, "validation": map[string]any{"target": "customer"}},
				map[string]any{"key": "bank", "name": "Bank", "type": "text", "required": true},
				map[string]any{"key": "number", "name": "Number", "type": "text", "required": true},
				map[string]any{"key": "primary", "name": "Primary", "type": "boolean"},
			}},
		},
		"actions": []any{register, legacy},
		"roles":   []any{map[string]any{"key": structuredActionRole, "name": "Structured admin", "permissions": permissions}},
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "structured-action-manifest.json")
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// structuredActionPermissions are the manifest-generated permissions the
// fixture role holds; the Identity fixture role adds business.access on top.
func structuredActionPermissions() []string {
	return []string{
		"customer.read", "customer.create", "customer.update", "customer_account.read", "customer_account.create",
		structuredRegisterKey, structuredLegacyKey,
	}
}

func newStructuredActionRuntime(t *testing.T, cfg config.Config) (*bootstrap.Runtime, *structuredActionHandler) {
	t.Helper()
	cfg.RuntimeAllowDevIdentityHeaders = true
	cfg = initializedIntegrationRuntimeConfig(cfg)
	if strings.TrimSpace(cfg.RuntimeVersion) == "" {
		cfg.RuntimeVersion = "integrationtest-runtime-v1"
	}
	factory := runtimetestkit.NewIdentityFactory(runtimetestkit.IdentityFixtureConfig{
		Roles:               []runtimetestkit.IdentityFixtureRole{integrationIdentityRole(structuredActionRole, "Structured admin", append([]string{"business.access"}, structuredActionPermissions()...), true)},
		Users:               []identitysdk.User{{ID: structuredActionUser, Name: "Structured user", Email: "structured@example.com", Status: "active"}},
		UserRoleAssignments: map[string][]string{structuredActionUser: {structuredActionRole}},
	})
	binding, err := factory.Open(t.Context(), identitysdk.ApplicationRef{
		WorkspaceID: identitysdk.WorkspaceID(cfg.IdentityWorkspaceID), ApplicationKey: identitysdk.ApplicationKey("domainry-runtime"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = binding.Close(context.Background()) })
	handler := &structuredActionHandler{}
	registry := runtimeext.NewBusinessHandlerRegistry()
	for _, spec := range []struct {
		key, suffix  string
		capabilities []runtimeext.ActionObjectCapability
	}{
		{structuredRegisterKey, "RegisterAccounts", []runtimeext.ActionObjectCapability{{ObjectKey: "customer", Operations: []string{"create"}}, {ObjectKey: "customer_account", Operations: []string{"create"}}}},
		{structuredLegacyKey, "LegacyRename", []runtimeext.ActionObjectCapability{{ObjectKey: "customer", Operations: []string{"get", "update"}}}},
	} {
		descriptor := runtimeext.HandlerDescriptor{
			ActionKey: spec.key, InputType: structuredInputTypeBase + spec.suffix + "Input", OutputType: structuredInputTypeBase + spec.suffix + "Output",
			InputContractSHA256: sourceOwnedFixtureContractHash(spec.key + ":input"), OutputContractSHA256: sourceOwnedFixtureContractHash(spec.key + ":output"),
			HandlerRevision: "structured-action-fixture-v1", ObjectCapabilities: spec.capabilities,
		}
		if err := registry.Register(&structuredActionBoundHandler{shared: handler, descriptor: descriptor}); err != nil {
			t.Fatal(err)
		}
	}
	registry.Freeze()
	runtime := bootstrap.NewWithBusinessHandlersAndScheduler(t.Context(), cfg, registry, binding, notificationmodule.NewFactory(notificationmodule.OptionsFromEnvironment()), schedulermodule.NewFactory(schedulermodule.OptionsFromEnvironment()), dataexchangefixture.NewFactory(), integrationmodule.NewFactory(), integrationAgentFactory())
	t.Cleanup(func() { _ = runtime.CloseContext(context.Background()) })
	return runtime, handler
}

// structuredActionBoundHandler gives each Action its own descriptor while the
// invocation evidence stays on one shared fixture.
type structuredActionBoundHandler struct {
	shared     *structuredActionHandler
	descriptor runtimeext.HandlerDescriptor
}

func (h *structuredActionBoundHandler) Descriptor() runtimeext.HandlerDescriptor { return h.descriptor }

func (h *structuredActionBoundHandler) Invoke(ctx context.Context, execution runtimeext.ActionExecution, raw json.RawMessage) (json.RawMessage, error) {
	return h.shared.Invoke(ctx, execution, raw)
}

type structuredActionResponse struct {
	status int
	body   map[string]any
	raw    string
}

func structuredActionRequest(t *testing.T, handler http.Handler, method, path string, body any, headers map[string]string) structuredActionResponse {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	}
	request := httptest.NewRequest(method, path, reader)
	request.Header.Set("Authorization", "Bearer "+runtimetestkit.IdentityFixtureAccessToken(structuredActionUser, structuredActionRole))
	request.Header.Set("X-Workspace-ID", "workspace-primary")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet {
		request.Header.Set("Idempotency-Key", method+":"+path+":"+time.Now().UTC().Format(time.RFC3339Nano))
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	response := structuredActionResponse{status: recorder.Code, raw: recorder.Body.String()}
	_ = json.Unmarshal(recorder.Body.Bytes(), &response.body)
	return response
}

func structuredRecordTotal(t *testing.T, handler http.Handler, objectKey string) int {
	t.Helper()
	response := structuredActionRequest(t, handler, http.MethodGet, "/records/"+objectKey+"?page=1&page_size=10", nil, nil)
	if response.status != http.StatusOK {
		t.Fatalf("list %s status=%d body=%s", objectKey, response.status, response.raw)
	}
	total, ok := response.body["total"].(float64)
	if !ok {
		t.Fatalf("list %s has no total: %s", objectKey, response.raw)
	}
	return int(total)
}

func TestStructuredActionInputReachesHandlerAndRejectsInvalidShapes(t *testing.T) {
	temp := t.TempDir()
	runtime, fixture := newStructuredActionRuntime(t, config.Config{
		AppLocale: "en-US", DatabaseDriver: "sqlite", DBPath: filepath.Join(temp, "runtime.db"), ManifestPath: structuredActionManifest(t, temp), UploadDir: filepath.Join(temp, "uploads"),
	})
	handler := runtime.Routes()
	path := "/records/customer/actions/" + structuredRegisterKey
	// Runtime seeds one generated baseline record per object; assertions use
	// deltas against that baseline.
	baseCustomers, baseAccounts := structuredRecordTotal(t, handler, "customer"), structuredRecordTotal(t, handler, "customer_account")

	catalog := structuredActionRequest(t, handler, http.MethodGet, "/records/customer/actions", nil, nil)
	if catalog.status != http.StatusOK || !strings.Contains(catalog.raw, `"repeated":true`) || !strings.Contains(catalog.raw, `"max_items":3`) || !strings.Contains(catalog.raw, `"fields":[`) {
		t.Fatalf("action disclosure lost the structured contract: status=%d body=%s", catalog.status, catalog.raw)
	}

	valid := map[string]any{
		"name": "Acme",
		"accounts": []any{
			map[string]any{"bank": "First Bank", "number": "0001", "primary": true},
			map[string]any{"bank": "Second Bank", "number": "0002"},
		},
		"steps": []any{
			map[string]any{"title": "Review", "required_approvals": 2, "assignees": []any{"structured_user"}},
			map[string]any{"title": "Sign", "assignees": []any{"structured_user", "structured_user"}},
		},
	}
	response := structuredActionRequest(t, handler, http.MethodPost, path, map[string]any{"data": valid}, nil)
	if response.status != http.StatusOK {
		t.Fatalf("valid structured invocation status=%d body=%s", response.status, response.raw)
	}
	fixture.mu.Lock()
	decoded := append([]structuredRegisterAccountsInput(nil), fixture.decoded...)
	rawInputs := append([]string(nil), fixture.rawInputs...)
	fixture.mu.Unlock()
	if len(decoded) != 1 {
		t.Fatalf("handler invocations=%d", len(decoded))
	}
	input := decoded[0]
	if input.Name != "Acme" || len(input.Accounts) != 2 || input.Accounts[0].Bank != "First Bank" || !input.Accounts[0].Primary || input.Accounts[1].Number != "0002" || input.Accounts[1].Primary {
		t.Fatalf("decoded accounts=%#v", input)
	}
	if len(input.Steps) != 2 || input.Steps[0].RequiredApprovals != 2 || !reflect.DeepEqual(input.Steps[1].Assignees, []string{"structured_user", "structured_user"}) || input.Steps[1].RequiredApprovals != 0 {
		t.Fatalf("decoded steps=%#v", input.Steps)
	}
	if strings.Contains(rawInputs[0], "expected_version") || !strings.Contains(rawInputs[0], `"accounts":[{"bank":"First Bank","number":"0001","primary":true}`) {
		t.Fatalf("handler raw input=%s", rawInputs[0])
	}
	output, _ := response.body["output"].(map[string]any)
	if output["customer_id"] == "" || output["step_count"] != float64(2) {
		t.Fatalf("action output=%#v", response.body)
	}
	if structuredRecordTotal(t, handler, "customer") != baseCustomers+1 || structuredRecordTotal(t, handler, "customer_account") != baseAccounts+2 {
		t.Fatal("parent and children were not written together")
	}

	cases := []struct {
		name      string
		mutate    func(map[string]any)
		code      string
		fieldPath string
	}{
		{"missing nested required array", func(m map[string]any) {
			m["steps"] = []any{map[string]any{"title": "Review", "assignees": []any{"structured_user"}}, map[string]any{"title": "Sign"}}
		}, "backend.validation.required", "steps[1].assignees"},
		{"unknown nested key", func(m map[string]any) {
			m["accounts"] = []any{map[string]any{"bank": "b", "number": "1", "nickname": "x"}}
		}, "backend.validation.unknown_field", "accounts[0].nickname"},
		{"array expected", func(m map[string]any) { m["accounts"] = "x" }, "backend.validation.array_expected", "accounts"},
		{"over max items", func(m map[string]any) {
			m["accounts"] = []any{map[string]any{"bank": "a", "number": "1"}, map[string]any{"bank": "b", "number": "2"}, map[string]any{"bank": "c", "number": "3"}, map[string]any{"bank": "d", "number": "4"}}
		}, "backend.validation.max_items", "accounts"},
		{"wrong nested leaf type", func(m map[string]any) {
			m["steps"] = []any{map[string]any{"title": "Review", "required_approvals": "many", "assignees": []any{"u"}}}
		}, "backend.validation.integer", "steps[0].required_approvals"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := map[string]any{}
			for key, value := range valid {
				payload[key] = value
			}
			tc.mutate(payload)
			response := structuredActionRequest(t, handler, http.MethodPost, path, map[string]any{"data": payload}, nil)
			if response.status != http.StatusBadRequest || response.body["code"] != tc.code || response.body["field_path"] != tc.fieldPath {
				t.Fatalf("status=%d body=%s", response.status, response.raw)
			}
			params, _ := response.body["params"].(map[string]any)
			if params["field"] != tc.fieldPath || params["object"] != structuredRegisterKey {
				t.Fatalf("params=%#v", params)
			}
			if tc.code == "backend.validation.max_items" && (params["limit"] != "3" || params["actual"] != "4") {
				t.Fatalf("max items params=%#v", params)
			}
		})
	}
	if structuredRecordTotal(t, handler, "customer") != baseCustomers+1 || structuredRecordTotal(t, handler, "customer_account") != baseAccounts+2 {
		t.Fatal("rejected invocations must not write records")
	}
}

func TestStructuredActionParentAndChildrenRollBackTogether(t *testing.T) {
	temp := t.TempDir()
	runtime, _ := newStructuredActionRuntime(t, config.Config{
		AppLocale: "en-US", DatabaseDriver: "sqlite", DBPath: filepath.Join(temp, "runtime.db"), ManifestPath: structuredActionManifest(t, temp), UploadDir: filepath.Join(temp, "uploads"),
	})
	handler := runtime.Routes()
	baseCustomers, baseAccounts := structuredRecordTotal(t, handler, "customer"), structuredRecordTotal(t, handler, "customer_account")
	payload := map[string]any{"data": map[string]any{
		"name":     "Rollback Co",
		"accounts": []any{map[string]any{"bank": "First", "number": "1"}, map[string]any{"bank": "Second", "number": "2"}},
	}}
	response := structuredActionRequest(t, handler, http.MethodPost, "/records/customer/actions/"+structuredRegisterKey, payload, map[string]string{actionmodel.AcceptanceFailureHeader: actionmodel.AcceptanceFailureBeforeCommit})
	if response.status != http.StatusInternalServerError || response.body["code"] != actionmodel.AcceptanceFailureInjectedCode {
		t.Fatalf("injected failure status=%d body=%s", response.status, response.raw)
	}
	if structuredRecordTotal(t, handler, "customer") != baseCustomers || structuredRecordTotal(t, handler, "customer_account") != baseAccounts {
		t.Fatal("parent or child rows survived the aborted Action transaction")
	}
	if listed := structuredActionRequest(t, handler, http.MethodGet, "/records/customer?page=1&page_size=10", nil, nil); strings.Contains(listed.raw, "Rollback Co") {
		t.Fatalf("aborted parent leaked into the customer table: %s", listed.raw)
	}
	response = structuredActionRequest(t, handler, http.MethodPost, "/records/customer/actions/"+structuredRegisterKey, payload, nil)
	if response.status != http.StatusOK {
		t.Fatalf("invocation without injection status=%d body=%s", response.status, response.raw)
	}
	if structuredRecordTotal(t, handler, "customer") != baseCustomers+1 || structuredRecordTotal(t, handler, "customer_account") != baseAccounts+2 {
		t.Fatal("parent and children must commit together")
	}
}

func TestLegacyScalarActionKeepsFlatPayloadBehaviour(t *testing.T) {
	temp := t.TempDir()
	runtime, _ := newStructuredActionRuntime(t, config.Config{
		AppLocale: "en-US", DatabaseDriver: "sqlite", DBPath: filepath.Join(temp, "runtime.db"), ManifestPath: structuredActionManifest(t, temp), UploadDir: filepath.Join(temp, "uploads"),
	})
	handler := runtime.Routes()
	created := structuredActionRequest(t, handler, http.MethodPost, "/records/customer", map[string]any{"data": map[string]any{"name": "Before"}}, nil)
	if created.status != http.StatusOK && created.status != http.StatusCreated {
		t.Fatalf("create customer status=%d body=%s", created.status, created.raw)
	}
	recordID, _ := created.body["id"].(string)
	path := "/records/customer/items/" + recordID + "/actions/" + structuredLegacyKey
	response := structuredActionRequest(t, handler, http.MethodPost, path, map[string]any{"data": map[string]any{"name": "After", "expected_version": 1}}, nil)
	if response.status != http.StatusOK {
		t.Fatalf("legacy invocation status=%d body=%s", response.status, response.raw)
	}
	record, _ := response.body["record"].(map[string]any)
	data, _ := record["data"].(map[string]any)
	if data["name"] != "After" {
		t.Fatalf("legacy action result=%s", response.raw)
	}
	response = structuredActionRequest(t, handler, http.MethodPost, path, map[string]any{"data": map[string]any{"name": "x", "nickname": "y"}}, nil)
	if response.status != http.StatusBadRequest || response.body["code"] != "backend.validation.unknown_field" || response.body["field_path"] != "nickname" {
		t.Fatalf("legacy unknown field status=%d body=%s", response.status, response.raw)
	}
	response = structuredActionRequest(t, handler, http.MethodPost, path, map[string]any{"data": map[string]any{"name": map[string]any{"first": "x"}}}, nil)
	if response.status != http.StatusBadRequest || response.body["code"] != "backend.validation.string" || response.body["field_path"] != "name" {
		t.Fatalf("legacy scalar rejects objects status=%d body=%s", response.status, response.raw)
	}
	response = structuredActionRequest(t, handler, http.MethodPost, path, map[string]any{"data": map[string]any{}}, nil)
	if response.status != http.StatusBadRequest || response.body["code"] != "backend.validation.required" || response.body["field_path"] != "name" {
		t.Fatalf("legacy required status=%d body=%s", response.status, response.raw)
	}
}
