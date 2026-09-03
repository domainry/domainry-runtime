package contract

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// RuntimeModelAPIContractVersion identifies the transport-free projection of
// the Runtime API that is safe to place in model context. RuntimeAPIContract is
// still the exact HTTP/client compatibility contract and must not be used as a
// model prompt.
const RuntimeModelAPIContractVersion = "runtime-model-api-v1"

type runtimeModelAPITransportDocument struct {
	Routes  map[string]runtimeModelAPITransportRoute `json:"routes"`
	Schemas map[string]json.RawMessage               `json:"schemas"`
}

type runtimeModelAPITransportRoute struct {
	Path          string            `json:"path"`
	Query         []string          `json:"query"`
	RequiredQuery []string          `json:"required_query"`
	Request       string            `json:"request"`
	Response      string            `json:"response"`
	Responses     map[string]string `json:"responses"`
}

type runtimeModelAPIOperation struct {
	Domain       string   `json:"domain"`
	Arguments    []string `json:"arguments,omitempty"`
	InputSchema  string   `json:"input_schema,omitempty"`
	ResultSchema string   `json:"result_schema,omitempty"`
}

type runtimeModelAPIIndex struct {
	ContractVersion string              `json:"contract_version"`
	ContractHash    string              `json:"contract_hash"`
	Domains         map[string][]string `json:"domains"`
}

type runtimeModelAPIProjection struct {
	ContractVersion string                              `json:"contract_version"`
	ContractHash    string                              `json:"contract_hash"`
	Operations      map[string]runtimeModelAPIOperation `json:"operations"`
	Schemas         map[string]json.RawMessage          `json:"schemas,omitempty"`
}

// RuntimeModelAPIContractHash changes when either the exact Runtime transport
// contract or this projection policy changes. It lets a consumer cache the
// compact index without receiving the full transport document.
func RuntimeModelAPIContractHash() string {
	digest := sha256.Sum256([]byte(RuntimeModelAPIContractVersion + ":" + RuntimeAPIContractHash()))
	return hex.EncodeToString(digest[:])
}

// RuntimeModelAPIContractIndex publishes only operation keys grouped by
// business domain. A model selects from this index before requesting detail.
func RuntimeModelAPIContractIndex() []byte {
	document, err := runtimeModelAPIContract()
	if err != nil {
		panic(err)
	}
	domains := map[string][]string{}
	for key, operation := range document.operations {
		domains[operation.Domain] = append(domains[operation.Domain], key)
	}
	for domain := range domains {
		sort.Strings(domains[domain])
	}
	payload, err := json.Marshal(runtimeModelAPIIndex{ContractVersion: RuntimeModelAPIContractVersion, ContractHash: RuntimeModelAPIContractHash(), Domains: domains})
	if err != nil {
		panic(err)
	}
	return payload
}

// RuntimeModelAPIContractProjection returns model-owned semantics for the
// explicitly selected operations. HTTP paths, methods, headers, status codes,
// idempotency mechanics, streams and follow-up download routes stay in the
// exact transport contract and never enter this projection.
func RuntimeModelAPIContractProjection(operationKeys ...string) ([]byte, error) {
	document, err := runtimeModelAPIContract()
	if err != nil {
		return nil, err
	}
	selected := map[string]runtimeModelAPIOperation{}
	referencedSchemas := map[string]bool{}
	for _, rawKey := range operationKeys {
		key := strings.TrimSpace(rawKey)
		operation, exists := document.operations[key]
		if !exists {
			return nil, fmt.Errorf("Runtime model API operation %q is unavailable", key)
		}
		selected[key] = operation
		if operation.InputSchema != "" {
			referencedSchemas[operation.InputSchema] = true
		}
		if operation.ResultSchema != "" && operation.ResultSchema != "empty" && operation.ResultSchema != "file" {
			referencedSchemas[operation.ResultSchema] = true
		}
	}
	schemas := runtimeModelAPISchemaClosure(document.schemas, referencedSchemas)
	payload, err := json.Marshal(runtimeModelAPIProjection{
		ContractVersion: RuntimeModelAPIContractVersion,
		ContractHash:    RuntimeModelAPIContractHash(),
		Operations:      selected,
		Schemas:         schemas,
	})
	if err != nil {
		return nil, err
	}
	return payload, nil
}

type runtimeModelAPIContractDocument struct {
	operations map[string]runtimeModelAPIOperation
	schemas    map[string]json.RawMessage
}

func runtimeModelAPIContract() (runtimeModelAPIContractDocument, error) {
	var transport runtimeModelAPITransportDocument
	if err := json.Unmarshal(RuntimeAPIContractDocument(), &transport); err != nil {
		return runtimeModelAPIContractDocument{}, fmt.Errorf("decode Runtime API contract: %w", err)
	}
	keys := make([]string, 0, len(transport.Routes))
	for key := range transport.Routes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	operations := map[string]runtimeModelAPIOperation{}
	for _, sourceKey := range keys {
		if runtimeModelAPITransportOnly(sourceKey) {
			continue
		}
		key := runtimeModelAPIOperationKey(sourceKey)
		route := transport.Routes[sourceKey]
		operation := runtimeModelAPIOperation{
			Domain:       runtimeModelAPIDomain(key),
			Arguments:    runtimeModelAPIArguments(route),
			InputSchema:  strings.TrimSpace(route.Request),
			ResultSchema: strings.TrimSpace(route.Response),
		}
		if key == "record_export" || key == "audit_event_export" {
			operation.ResultSchema = "file"
		}
		if operation.ResultSchema == "" && len(route.Responses) == 1 {
			for _, result := range route.Responses {
				operation.ResultSchema = strings.TrimSpace(result)
			}
		}
		if existing, exists := operations[key]; exists {
			if !runtimeModelAPIOperationsEqual(existing, operation) {
				return runtimeModelAPIContractDocument{}, fmt.Errorf("Runtime model API aliases for %q disagree", key)
			}
			continue
		}
		operations[key] = operation
	}
	return runtimeModelAPIContractDocument{operations: operations, schemas: transport.Schemas}, nil
}

func runtimeModelAPITransportOnly(key string) bool {
	switch key {
	case "business_event_stream", "business_notification_stream", "business_record_sync_stream",
		"portal_notification_stream", "business_audit_event_export_download", "record_export_download", "file_download":
		return true
	default:
		return false
	}
}

func runtimeModelAPIOperationKey(key string) string {
	for _, prefix := range []string{"business_notification_", "portal_notification_"} {
		if strings.HasPrefix(key, prefix) {
			return "notification_" + strings.TrimPrefix(key, prefix)
		}
	}
	if key == "portal_workflow_run" {
		return "workflow_run"
	}
	if key == "business_audit_event_export_prepare" {
		return "audit_event_export"
	}
	return key
}

func runtimeModelAPIDomain(key string) string {
	switch {
	case strings.HasPrefix(key, "notification_"):
		return "notification"
	case strings.HasPrefix(key, "workflow_"):
		return "workflow"
	case strings.HasPrefix(key, "business_audit_") || strings.HasPrefix(key, "audit_event_"):
		return "audit"
	case strings.HasPrefix(key, "file_"):
		return "files"
	case strings.HasPrefix(key, "publication_"):
		return "publication"
	case strings.HasPrefix(key, "object_") || strings.HasPrefix(key, "record_") || strings.HasPrefix(key, "action_"):
		return "records"
	default:
		return "runtime"
	}
}

func runtimeModelAPIArguments(route runtimeModelAPITransportRoute) []string {
	set := map[string]bool{}
	path := route.Path
	for {
		start := strings.Index(path, "{")
		if start < 0 {
			break
		}
		path = path[start+1:]
		end := strings.Index(path, "}")
		if end < 0 {
			break
		}
		if key := strings.TrimSpace(path[:end]); key != "" {
			set[key] = true
		}
		path = path[end+1:]
	}
	for _, key := range append(append([]string(nil), route.Query...), route.RequiredQuery...) {
		if key = strings.TrimSpace(key); key != "" {
			set[key] = true
		}
	}
	result := make([]string, 0, len(set))
	for key := range set {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func runtimeModelAPIOperationsEqual(left, right runtimeModelAPIOperation) bool {
	leftJSON, _ := json.Marshal(left)
	rightJSON, _ := json.Marshal(right)
	return string(leftJSON) == string(rightJSON)
}

func runtimeModelAPISchemaClosure(schemas map[string]json.RawMessage, selected map[string]bool) map[string]json.RawMessage {
	result := map[string]json.RawMessage{}
	queue := make([]string, 0, len(selected))
	for key := range selected {
		queue = append(queue, key)
	}
	sort.Strings(queue)
	for len(queue) > 0 {
		key := queue[0]
		queue = queue[1:]
		if _, exists := result[key]; exists {
			continue
		}
		raw, exists := schemas[key]
		if !exists {
			continue
		}
		result[key] = runtimeModelAPISanitizeSchema(key, raw)
		var value any
		if json.Unmarshal(raw, &value) != nil {
			continue
		}
		for candidate := range schemas {
			if runtimeModelAPIContainsString(value, candidate) {
				queue = append(queue, candidate)
			}
		}
	}
	return result
}

func runtimeModelAPISanitizeSchema(key string, raw json.RawMessage) json.RawMessage {
	if key != "notification" {
		return append(json.RawMessage(nil), raw...)
	}
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil {
		return append(json.RawMessage(nil), raw...)
	}
	for _, collection := range []string{"required", "optional"} {
		items, _ := value[collection].([]any)
		filtered := make([]any, 0, len(items))
		for _, item := range items {
			if strings.TrimSpace(fmt.Sprint(item)) != "surface" {
				filtered = append(filtered, item)
			}
		}
		value[collection] = filtered
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return append(json.RawMessage(nil), raw...)
	}
	return payload
}

func runtimeModelAPIContainsString(value any, wanted string) bool {
	switch typed := value.(type) {
	case string:
		return typed == wanted
	case []any:
		for _, item := range typed {
			if runtimeModelAPIContainsString(item, wanted) {
				return true
			}
		}
	case map[string]any:
		for _, item := range typed {
			if runtimeModelAPIContainsString(item, wanted) {
				return true
			}
		}
	}
	return false
}
