package openapi_test

import (
	"encoding/json"
	"strings"
	"testing"

	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	openapihttp "github.com/domainry/domainry-runtime/runtime/transport/http/openapi"
)

func TestEveryAdminBusinessReuseRouteHasOpenAPIOperation(t *testing.T) {
	var contract struct {
		AdminBusinessReuse struct {
			Routes []struct {
				EndpointIdentity string `json:"endpoint_identity"`
			} `json:"routes"`
		} `json:"admin_business_reuse"`
	}
	if err := json.Unmarshal(capabilitycontract.RuntimeAPIContractDocument(), &contract); err != nil {
		t.Fatal(err)
	}
	paths := openapihttp.Build(metadatamodel.ApplicationSchemaSnapshot{})["paths"].(map[string]any)
	for _, route := range contract.AdminBusinessReuse.Routes {
		method, path, ok := strings.Cut(route.EndpointIdentity, " ")
		if !ok {
			t.Fatalf("invalid endpoint identity %q", route.EndpointIdentity)
		}
		pathItem, ok := paths[path].(map[string]any)
		if !ok {
			t.Errorf("missing OpenAPI path for %s", route.EndpointIdentity)
			continue
		}
		operation, ok := pathItem[strings.ToLower(method)].(map[string]any)
		if !ok {
			t.Errorf("missing OpenAPI operation for %s", route.EndpointIdentity)
			continue
		}
		if strings.TrimSpace(operation["operationId"].(string)) == "" || operation["responses"] == nil {
			t.Errorf("incomplete OpenAPI operation for %s", route.EndpointIdentity)
		}
	}
}
