package http

import (
	"net/http"
	"strings"
	"testing"

	endpointmodel "github.com/domainry/domainry-runtime/runtime/domain/endpoint/model"
)

func TestPublicRouteInventoryCannotExposeRuntimeOpsEndpoints(t *testing.T) {
	publicExposure, ok := listenerExposure(ListenerRouteGroupPublic)
	if !ok {
		t.Fatal("public listener exposure is missing")
	}
	for route, contract := range runtimeEndpointContracts {
		_, path, ok := strings.Cut(route, " ")
		if !ok || !strings.HasPrefix(strings.TrimSpace(path), "/operations") {
			continue
		}
		if endpointVisibleOnListener(contract, publicExposure) {
			t.Fatalf("public listener exposes Ops route %q with exposures %v", route, contract.ListenerExposures)
		}
	}
	if ListenerRouteGroupEndpointCount(ListenerRouteGroupPublic) == 0 ||
		ListenerRouteGroupEndpointCount(ListenerRouteGroupOps) == 0 {
		t.Fatalf("listener endpoint counts public=%d ops=%d",
			ListenerRouteGroupEndpointCount(ListenerRouteGroupPublic),
			ListenerRouteGroupEndpointCount(ListenerRouteGroupOps),
		)
	}
}

func TestOpenAPIProjectionExcludesCrossListenerOperations(t *testing.T) {
	document := openAPIDocumentFromCompiledEndpointInventory()
	fullPathCount := len(document["paths"].(map[string]any))

	publicDocument := cloneOpenAPIDocument(document)
	if err := projectOpenAPIForListenerGroup(publicDocument, ListenerRouteGroupPublic); err != nil {
		t.Fatal(err)
	}
	publicPaths := publicDocument["paths"].(map[string]any)
	if _, exists := publicPaths["/operations"]; exists {
		t.Fatal("public OpenAPI contains /operations")
	}
	if _, exists := publicPaths["/scheduler/state"]; exists {
		t.Fatal("public OpenAPI contains scheduler Ops state")
	}
	if _, exists := publicPaths["/records/{objectKey}"]; !exists {
		t.Fatal("public OpenAPI lost Business record API")
	}
	if len(publicPaths) >= fullPathCount {
		t.Fatalf("public projection did not reduce paths: public=%d full=%d", len(publicPaths), fullPathCount)
	}

	opsDocument := cloneOpenAPIDocument(document)
	if err := projectOpenAPIForListenerGroup(opsDocument, ListenerRouteGroupOps); err != nil {
		t.Fatal(err)
	}
	opsPaths := opsDocument["paths"].(map[string]any)
	if _, exists := opsPaths["/operations"]; !exists {
		t.Fatal("Ops OpenAPI does not contain /operations")
	}
	if item, exists := opsPaths["/records/{objectKey}"]; exists {
		if _, hasGET := item.(map[string]any)["get"]; hasGET {
			t.Fatal("Ops OpenAPI contains Business record list")
		}
	}
}

func TestOpenAPIProjectionUsesModuleRouteExposureWithoutRuntimeEndpointContract(t *testing.T) {
	operation := map[string]any{"x-domainry-module-route": map[string]any{"exposures": []any{"ops"}}}
	newDocument := func() map[string]any {
		return map[string]any{"paths": map[string]any{
			"/monitoring/metrics": map[string]any{"get": operation},
		}}
	}

	ops := newDocument()
	if err := projectOpenAPIForListenerGroup(ops, ListenerRouteGroupOps); err != nil {
		t.Fatal(err)
	}
	if _, exists := ops["paths"].(map[string]any)["/monitoring/metrics"]; !exists {
		t.Fatal("Ops projection removed the Monitoring module route")
	}

	tenantAdmin := newDocument()
	if err := projectOpenAPIForListenerGroup(tenantAdmin, ListenerRouteGroupManagement); err != nil {
		t.Fatal(err)
	}
	if _, exists := tenantAdmin["paths"].(map[string]any)["/monitoring/metrics"]; exists {
		t.Fatal("management projection exposed the Ops-only Monitoring module route")
	}
}

func openAPIDocumentFromCompiledEndpointInventory() map[string]any {
	paths := map[string]any{}
	for route, contract := range runtimeEndpointContracts {
		method, path, ok := strings.Cut(route, " ")
		if !ok {
			continue
		}
		switch method {
		case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		default:
			continue
		}
		item, _ := paths[path].(map[string]any)
		if item == nil {
			item = map[string]any{}
			paths[path] = item
		}
		item[strings.ToLower(method)] = map[string]any{
			"x-domainry-endpoint-contract": contract,
		}
	}
	return map[string]any{"openapi": "3.1.0", "paths": paths}
}

func cloneOpenAPIDocument(document map[string]any) map[string]any {
	paths := map[string]any{}
	for path, rawItem := range document["paths"].(map[string]any) {
		item := map[string]any{}
		for method, operation := range rawItem.(map[string]any) {
			item[method] = operation
		}
		paths[path] = item
	}
	return map[string]any{"openapi": document["openapi"], "paths": paths}
}

func TestOpsListenerUsesIndependentExposureClassification(t *testing.T) {
	exposure, ok := listenerExposure(ListenerRouteGroupOps)
	if !ok || exposure != endpointmodel.ListenerExposureOps {
		t.Fatalf("Ops exposure=%q known=%v", exposure, ok)
	}
}
