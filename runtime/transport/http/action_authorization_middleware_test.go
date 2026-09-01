package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	actioncontract "github.com/domainry/domainry-foundation/action"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	actionservice "github.com/domainry/domainry-runtime/runtime/domain/action/service"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

func TestRuntimeActionGateResolvesConcreteObjectAndAuthoredActions(t *testing.T) {
	readOnly := definitionmodel.ObjectCapabilitySet{Read: true}
	registry, err := actionservice.BuildAuthorizationRegistry(actionservice.AuthorizationRegistryInput{
		ApplicationKey: "orders",
		Snapshot: appschemamodel.ApplicationSchemaSnapshot{
			Objects: []definitionmodel.ObjectSchema{{Key: "customer", Capabilities: &readOnly}},
			Actions: []definitionmodel.ActionSchema{
				{Key: "customer.approve", ObjectKey: "customer", Label: "Approve customer", Kind: definitionmodel.ActionKindRecordOperation, AuditEvent: "customer.approved"},
				{Key: "invoice.approve", ObjectKey: "invoice", Label: "Approve invoice", Kind: definitionmodel.ActionKindRecordOperation, AuditEvent: "invoice.approved"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	router := &HTTPRouter{authorizationActions: func() *actioncontract.Registry { return registry }}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /objects/{objectKey}/records", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("POST /objects/{objectKey}/records", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("POST /objects/{objectKey}/actions/{actionKey}/run", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	gate := router.withActionAuthorization(mux, mux)
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, accessfixture.Bundle{
		Permissions: []string{"customer.read", "customer.create", "customer.approve", "invoice.approve"},
	})

	tests := []struct {
		name, method, path string
		want               int
	}{
		{name: "exact object read", method: http.MethodGet, path: "/objects/customer/records", want: http.StatusNoContent},
		{name: "unsupported create capability", method: http.MethodPost, path: "/objects/customer/records", want: http.StatusForbidden},
		{name: "unknown object", method: http.MethodGet, path: "/objects/invoice/records", want: http.StatusForbidden},
		{name: "exact authored action", method: http.MethodPost, path: "/objects/customer/actions/customer.approve/run", want: http.StatusNoContent},
		{name: "cross object authored action", method: http.MethodPost, path: "/objects/customer/actions/invoice.approve/run", want: http.StatusForbidden},
		{name: "no short action alias", method: http.MethodPost, path: "/objects/customer/actions/approve/run", want: http.StatusForbidden},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := requestWithPrincipal(httptest.NewRequest(test.method, test.path, nil), principal)
			response := httptest.NewRecorder()
			gate.ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestMatchedRouteValueRunsBeforeServeMuxDispatch(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/objects/customer/actions/customer.approve/run", nil)
	if request.PathValue("objectKey") != "" {
		t.Fatal("test requires a request that has not been dispatched by ServeMux")
	}
	template := "/objects/{objectKey}/actions/{actionKey}/run"
	if got := matchedRouteValue(template, request.URL.Path, "objectKey"); got != "customer" {
		t.Fatalf("object key=%q", got)
	}
	if got := matchedRouteValue(template, request.URL.Path, "actionKey"); got != "customer.approve" {
		t.Fatalf("action key=%q", got)
	}
	if got := matchedRouteValue(template, "/objects/customer/records", "objectKey"); got != "" {
		t.Fatalf("mismatched route value=%q", got)
	}
}
