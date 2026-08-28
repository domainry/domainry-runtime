package http

import (
	"net/http"
	"strings"
	"testing"

	surfacemodel "github.com/domainry/domainry-runtime/runtime/domain/surface/model"
)

func TestPublicRouteInventoryCannotExposeRuntimeOpsEndpoints(t *testing.T) {
	publicTargets := routeGroupSurfaces(SurfaceRouteGroupPublic)
	if len(publicTargets) != 2 {
		t.Fatalf("public targets=%v", publicTargets)
	}
	for route, surfaces := range runtimeSurfaceRoutePolicies {
		_, path, ok := strings.Cut(route, " ")
		if !ok || !strings.HasPrefix(strings.TrimSpace(path), "/operations") {
			continue
		}
		if routeVisibleOnGroup(surfaces, publicTargets) {
			t.Fatalf("public listener exposes Ops route %q with surfaces %v", route, surfaces)
		}
	}
	if SurfaceRouteGroupEndpointCount(SurfaceRouteGroupPublic) == 0 ||
		SurfaceRouteGroupEndpointCount(SurfaceRouteGroupOps) == 0 {
		t.Fatalf("surface endpoint counts public=%d ops=%d",
			SurfaceRouteGroupEndpointCount(SurfaceRouteGroupPublic),
			SurfaceRouteGroupEndpointCount(SurfaceRouteGroupOps),
		)
	}
}

func TestOpenAPIProjectionExcludesCrossSurfaceOperations(t *testing.T) {
	document := openAPIDocumentFromCompiledSurfaceInventory()
	fullPathCount := len(document["paths"].(map[string]any))

	publicDocument := cloneOpenAPIDocument(document)
	if err := projectOpenAPIForSurfaceGroup(publicDocument, SurfaceRouteGroupPublic); err != nil {
		t.Fatal(err)
	}
	publicPaths := publicDocument["paths"].(map[string]any)
	if _, exists := publicPaths["/operations"]; exists {
		t.Fatal("public OpenAPI contains /operations")
	}
	if _, exists := publicPaths["/operations/scheduler/state"]; exists {
		t.Fatal("public OpenAPI contains scheduler Ops state")
	}
	if _, exists := publicPaths["/objects/{objectKey}/records"]; !exists {
		t.Fatal("public OpenAPI lost Business record API")
	}
	if len(publicPaths) >= fullPathCount {
		t.Fatalf("public projection did not reduce paths: public=%d full=%d", len(publicPaths), fullPathCount)
	}

	opsDocument := cloneOpenAPIDocument(document)
	if err := projectOpenAPIForSurfaceGroup(opsDocument, SurfaceRouteGroupOps); err != nil {
		t.Fatal(err)
	}
	opsPaths := opsDocument["paths"].(map[string]any)
	if _, exists := opsPaths["/operations"]; !exists {
		t.Fatal("Ops OpenAPI does not contain /operations")
	}
	if item, exists := opsPaths["/objects/{objectKey}/records"]; exists {
		if _, hasGET := item.(map[string]any)["get"]; hasGET {
			t.Fatal("Ops OpenAPI contains Business record list")
		}
	}
}

func openAPIDocumentFromCompiledSurfaceInventory() map[string]any {
	paths := map[string]any{}
	for route := range runtimeSurfaceRoutePolicies {
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
			"x-domainry-surfaces": runtimeSurfaceRoutePolicies[route],
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

func TestOpsListenerRetainsItsSurfaceClassification(t *testing.T) {
	opsTargets := routeGroupSurfaces(SurfaceRouteGroupOps)
	if len(opsTargets) != 1 || opsTargets[0] != surfacemodel.ProductSurfaceAdminConsole {
		t.Fatalf("Ops targets=%v", opsTargets)
	}
}
