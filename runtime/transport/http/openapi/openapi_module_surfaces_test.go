package openapi

import (
	"net/http"
	"testing"

	"github.com/domainry/domainry-foundation/modulehttp"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
)

type openAPIModuleSurface struct {
	owner  string
	routes []modulehttp.Route
}

func (openAPIModuleSurface) ContractVersion() string { return modulehttp.ContractVersion }
func (s openAPIModuleSurface) Owner() string         { return s.owner }
func (openAPIModuleSurface) Name() string            { return "test" }
func (s openAPIModuleSurface) Routes() []modulehttp.Route {
	return append([]modulehttp.Route(nil), s.routes...)
}
func (openAPIModuleSurface) Handler() http.Handler { return http.NotFoundHandler() }

func TestOpenAPIModuleOwnershipComesFromSurfaceRoutes(t *testing.T) {
	surface := openAPIModuleSurface{owner: "example-module", routes: []modulehttp.Route{{
		Pattern: "GET /operations/monitoring/metrics", Exposures: []modulehttp.Exposure{modulehttp.ExposureOps},
		Authentication: modulehttp.AuthenticationAuthenticated, PrincipalOnly: true,
	}}}
	spec := BuildWithModuleHTTPSurfaces(appschemamodel.ApplicationSchemaSnapshot{}, "Domainry", []modulehttp.Surface{surface})
	paths := spec["paths"].(map[string]any)
	operation := paths["/operations/monitoring/metrics"].(map[string]any)["get"].(map[string]any)
	if operation["x-domainry-module-owner"] != "example-module" {
		t.Fatalf("module owner=%v", operation["x-domainry-module-owner"])
	}

	withoutSurfaces := Build(appschemamodel.ApplicationSchemaSnapshot{})
	operation = withoutSurfaces["paths"].(map[string]any)["/operations/monitoring/metrics"].(map[string]any)["get"].(map[string]any)
	if _, hardcoded := operation["x-domainry-module-owner"]; hardcoded {
		t.Fatal("OpenAPI must not infer a module owner from a path")
	}
}

var _ modulehttp.Surface = openAPIModuleSurface{}
