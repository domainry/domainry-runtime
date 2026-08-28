package contract

// Admin reuse is a capability-owner contract; the Admin frontend is not an authorization owner.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

type adminBusinessReuseRoute struct {
	EndpointIdentity    string   `json:"endpoint_identity"`
	Classification      string   `json:"classification"`
	ExposureClass       string   `json:"exposure_class"`
	TargetSurfaces      []string `json:"target_surfaces"`
	PermissionPolicyRef string   `json:"permission_policy_ref"`
}

type adminBusinessReuseDocument struct {
	ContractVersion string `json:"contract_version"`
	Totals          struct {
		AdminEndpoints   int `json:"admin_endpoints"`
		BusinessReusable int `json:"business_reusable"`
		OperationsOnly   int `json:"operations_only"`
	} `json:"totals"`
	Endpoints []adminBusinessReuseRoute `json:"endpoints"`
}

func TestAdminCapabilityInventorySeparatesBusinessReuseFromRuntimeOperations(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..", "..", "domainry-plane")
	payload, err := os.ReadFile(filepath.Join(root, "frontend", "domainry-admin", "contracts", "runtime-admin-business-reuse-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var inventory adminBusinessReuseDocument
	if err := json.Unmarshal(payload, &inventory); err != nil {
		t.Fatal(err)
	}
	if inventory.ContractVersion != "runtime-admin-business-reuse-v1" ||
		inventory.Totals.AdminEndpoints != len(inventory.Endpoints) ||
		inventory.Totals.BusinessReusable+inventory.Totals.OperationsOnly != inventory.Totals.AdminEndpoints {
		t.Fatalf("invalid Admin reuse totals: %+v endpoints=%d", inventory.Totals, len(inventory.Endpoints))
	}
	for _, route := range inventory.Endpoints {
		if route.EndpointIdentity == "" || route.PermissionPolicyRef == "" {
			t.Fatalf("incomplete Admin route inventory: %+v", route)
		}
		business := containsString(route.TargetSurfaces, "business_workspace")
		switch route.Classification {
		case "business_reusable":
			if !business || route.ExposureClass == "admin_private" {
				t.Fatalf("business-reusable route is not projected to Business Workspace: %+v", route)
			}
		case "operations_only":
			if business || route.ExposureClass != "admin_private" {
				t.Fatalf("operations exception is not based on admin_private ownership: %+v", route)
			}
		default:
			t.Fatalf("unknown Admin reuse classification: %+v", route)
		}
	}
}

func TestRuntimeAPIContractPublishesEveryReusableAdminCapability(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..", "..", "domainry-plane")
	payload, err := os.ReadFile(filepath.Join(root, "frontend", "domainry-admin", "contracts", "runtime-admin-business-reuse-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var inventory adminBusinessReuseDocument
	if err := json.Unmarshal(payload, &inventory); err != nil {
		t.Fatal(err)
	}

	var api struct {
		AdminBusinessReuse struct {
			ContractVersion string                    `json:"contract_version"`
			Transport       string                    `json:"transport"`
			Routes          []adminBusinessReuseRoute `json:"routes"`
		} `json:"admin_business_reuse"`
	}
	if err := json.Unmarshal(RuntimeAPIContractDocument(), &api); err != nil {
		t.Fatal(err)
	}
	if api.AdminBusinessReuse.ContractVersion != inventory.ContractVersion ||
		api.AdminBusinessReuse.Transport != "RuntimeClient.request/requestWithResponse" {
		t.Fatalf("unexpected Admin Business contract header: %+v", api.AdminBusinessReuse)
	}

	want := reusableAdminEndpointIdentities(inventory.Endpoints)
	got := reusableAdminEndpointIdentities(api.AdminBusinessReuse.Routes)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Runtime API Admin reuse routes drifted\ngot=%v\nwant=%v", got, want)
	}
}

func reusableAdminEndpointIdentities(routes []adminBusinessReuseRoute) []string {
	identities := make([]string, 0, len(routes))
	for _, route := range routes {
		if route.Classification == "business_reusable" {
			identities = append(identities, route.EndpointIdentity)
		}
	}
	sort.Strings(identities)
	return identities
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
