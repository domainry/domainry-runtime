package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	actioncontract "github.com/domainry/domainry-foundation/action"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	runtimeactioncontract "github.com/domainry/domainry-runtime/runtime/domain/action/contract"
	actionservice "github.com/domainry/domainry-runtime/runtime/domain/action/service"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	endpointmodel "github.com/domainry/domainry-runtime/runtime/domain/endpoint/model"
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
			Workflows: []definitionmodel.WorkflowSchema{
				{Key: "order.approval", Name: "Order approval", Enabled: true},
				{Key: "order.rejection", Name: "Order rejection", Enabled: true},
				{Key: "order.disabled", Name: "Disabled order flow", Enabled: false},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	router := &HTTPRouter{authorizationActions: func() *actioncontract.Registry { return registry }}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /records/{objectKey}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("POST /records/{objectKey}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("POST /records/{objectKey}/actions/{actionKey}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("POST /workflow/definitions/{workflowKey}/run", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	gate := router.withActionAuthorization(mux, mux)
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, accessfixture.Bundle{
		Permissions: []string{"customer.read", "customer.create", "customer.approve", "invoice.approve", "workflow.order.approval.run"},
	})

	tests := []struct {
		name, method, path string
		want               int
	}{
		{name: "exact object read", method: http.MethodGet, path: "/records/customer", want: http.StatusNoContent},
		{name: "unsupported create capability", method: http.MethodPost, path: "/records/customer", want: http.StatusForbidden},
		{name: "unknown object", method: http.MethodGet, path: "/records/invoice", want: http.StatusForbidden},
		{name: "exact authored action", method: http.MethodPost, path: "/records/customer/actions/customer.approve", want: http.StatusNoContent},
		{name: "cross object authored action", method: http.MethodPost, path: "/records/customer/actions/invoice.approve", want: http.StatusForbidden},
		{name: "no short action alias", method: http.MethodPost, path: "/records/customer/actions/approve", want: http.StatusForbidden},
		{name: "exact concrete workflow Action", method: http.MethodPost, path: "/workflow/definitions/order.approval/run", want: http.StatusNoContent},
		{name: "sibling workflow Action is not granted", method: http.MethodPost, path: "/workflow/definitions/order.rejection/run", want: http.StatusForbidden},
		{name: "disabled workflow has no Action", method: http.MethodPost, path: "/workflow/definitions/order.disabled/run", want: http.StatusForbidden},
		{name: "unknown workflow has no Action", method: http.MethodPost, path: "/workflow/definitions/order.unknown/run", want: http.StatusForbidden},
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

func TestRuntimeActionGatePropagatesOnlyTheAuthorizedExactAction(t *testing.T) {
	registry, err := actionservice.BuildAuthorizationRegistry(actionservice.AuthorizationRegistryInput{
		ApplicationKey: "runtime", EndpointContracts: endpointmodel.EndpointContracts,
	})
	if err != nil {
		t.Fatal(err)
	}
	router := &HTTPRouter{authorizationActions: func() *actioncontract.Registry { return registry }}
	mux := http.NewServeMux()
	observed := ""
	mux.HandleFunc("POST /records/{objectKey}/items/{recordID}/profile/deactivate", func(w http.ResponseWriter, r *http.Request) {
		definition, ok := runtimeactioncontract.AuthorizedActionFromContext(r.Context())
		if !ok {
			t.Fatal("authorized Action was not propagated")
		}
		observed = definition.Key
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /records/{objectKey}/items/{recordID}/profile/reactivate", func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("sibling Action reached its handler")
	})
	gate := router.withActionAuthorization(mux, mux)
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, accessfixture.Bundle{
		Permissions: []string{"runtime.records.deactivate_business_profile"},
	})

	request := requestWithPrincipal(httptest.NewRequest(http.MethodPost, "/records/customer/items/customer-1/profile/deactivate", nil), principal)
	response := httptest.NewRecorder()
	gate.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || observed != "runtime.records.deactivate_business_profile" {
		t.Fatalf("status=%d observed=%q body=%s", response.Code, observed, response.Body.String())
	}

	request = requestWithPrincipal(httptest.NewRequest(http.MethodPost, "/records/customer/items/customer-1/profile/reactivate", nil), principal)
	response = httptest.NewRecorder()
	gate.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || observed != "runtime.records.deactivate_business_profile" {
		t.Fatalf("sibling status=%d observed=%q body=%s", response.Code, observed, response.Body.String())
	}
}

func TestMatchedRouteValueRunsBeforeServeMuxDispatch(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/records/customer/actions/customer.approve", nil)
	if request.PathValue("objectKey") != "" {
		t.Fatal("test requires a request that has not been dispatched by ServeMux")
	}
	template := "/records/{objectKey}/actions/{actionKey}"
	if got := matchedRouteValue(template, request.URL.Path, "objectKey"); got != "customer" {
		t.Fatalf("object key=%q", got)
	}
	if got := matchedRouteValue(template, request.URL.Path, "actionKey"); got != "customer.approve" {
		t.Fatalf("action key=%q", got)
	}
	if got := matchedRouteValue(template, "/records/customer", "objectKey"); got != "" {
		t.Fatalf("mismatched route value=%q", got)
	}
}
