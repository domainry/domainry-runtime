package contract

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// The source generator referenced by capability_runtime_api_contract.go is not
// shipped in this repository. Keep its generated document immutable and derive
// the public contract by removing endpoints retired with online metadata
// authoring. This can be deleted when the generator is restored and rerun.
var projectedRuntimeAPIContractDocument, projectedRuntimeAPIContractHash = projectRuntimeAPIContract([]byte(runtimeAPIContractDocument))

func projectRuntimeAPIContract(source []byte) ([]byte, string) {
	var document map[string]any
	if err := json.Unmarshal(source, &document); err != nil {
		panic(fmt.Sprintf("decode generated Runtime API contract: %v", err))
	}
	document = filterRetiredRuntimeEndpoints(document).(map[string]any)
	document["contract_hash"] = ""
	canonical, err := json.Marshal(document)
	if err != nil {
		panic(fmt.Sprintf("encode projected Runtime API contract: %v", err))
	}
	digest := sha256.Sum256(canonical)
	hash := hex.EncodeToString(digest[:])
	document["contract_hash"] = hash
	projected, err := json.Marshal(document)
	if err != nil {
		panic(fmt.Sprintf("encode hashed Runtime API contract: %v", err))
	}
	return projected, hash
}

func filterRetiredRuntimeEndpoints(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if route, ok := child.(map[string]any); ok {
				method, _ := route["method"].(string)
				path, _ := route["path"].(string)
				if retiredRuntimeEndpoint(strings.ToUpper(method), path) {
					delete(typed, key)
					continue
				}
			}
			typed[key] = filterRetiredRuntimeEndpoints(child)
		}
		return typed
	case []any:
		kept := make([]any, 0, len(typed))
		for _, child := range typed {
			if object, objectOK := child.(map[string]any); objectOK {
				if endpoint, endpointOK := object["endpoint_identity"].(string); endpointOK {
					method, path, found := strings.Cut(endpoint, " ")
					if found && retiredRuntimeEndpoint(method, path) {
						continue
					}
				}
			}
			kept = append(kept, filterRetiredRuntimeEndpoints(child))
		}
		return kept
	default:
		return value
	}
}

func retiredRuntimeEndpoint(method, path string) bool {
	if strings.HasPrefix(path, "/business-seeds/") {
		return true
	}
	if strings.HasPrefix(path, "/tenant-admin/change-plans") || path == "/domain-maintenance/rollback-policy" {
		return true
	}
	if method == "GET" && path == "/automation-rules/{ruleKey}/versions" {
		return true
	}
	if method == "GET" && path == "/tenant-admin/scheduler/definitions/{definitionID}/versions" {
		return true
	}
	switch path {
	case "/frontend-capability-manifest":
		return method == "PUT"
	case "/tenant-admin/metadata/manifests/validate",
		"/tenant-admin/metadata/manifests/review",
		"/tenant-admin/metadata/manifests/apply":
		return true
	case "/tenant-admin/metadata/reload",
		"/tenant-admin/metadata/localized-texts/import",
		"/tenant-admin/metadata/definitions/{resourceType}/{resourceKey}/versions",
		"/tenant-admin/metadata/definitions/{resourceType}/{resourceKey}/diff":
		return true
	case "/tenant-admin/metadata/localized-texts":
		return method == "PUT"
	default:
		return false
	}
}
