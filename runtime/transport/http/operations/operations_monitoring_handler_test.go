package operations

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

type monitoringMetricsStub struct{}

func (monitoringMetricsStub) Metrics(context.Context) map[string]any {
	return map[string]any{"runtime_id": "runtime-1", "objects": 2}
}

func TestMonitoringMetricsUsesAuthorizedMonitoringBinding(t *testing.T) {
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "operator"}}, accessfixture.Bundle{Permissions: []string{"runtime_ops.capability_status.read"}})
	handler := NewOperationsHandler(OperationsDependencies{
		Monitoring: monitoringMetricsStub{}, Principal: func(*http.Request) principalmodel.Principal { return principal },
		WriteJSON: func(w http.ResponseWriter, status int, value any) {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(value)
		},
	})
	response := httptest.NewRecorder()
	handler.monitoringMetrics(response, httptest.NewRequest(http.MethodGet, "/operations/monitoring/metrics", nil))
	if response.Code != http.StatusOK || response.Body.String() != "{\"objects\":2,\"runtime_id\":\"runtime-1\"}\n" {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestMonitoringMetricsRejectsUnauthorizedPrincipal(t *testing.T) {
	handler := NewOperationsHandler(OperationsDependencies{
		Monitoring: monitoringMetricsStub{}, Principal: func(*http.Request) principalmodel.Principal { return principalmodel.Principal{} },
		WriteJSON: func(w http.ResponseWriter, status int, _ any) { w.WriteHeader(status) },
	})
	response := httptest.NewRecorder()
	handler.monitoringMetrics(response, httptest.NewRequest(http.MethodGet, "/operations/monitoring/metrics", nil))
	if response.Code != http.StatusForbidden {
		t.Fatalf("status=%d", response.Code)
	}
}
