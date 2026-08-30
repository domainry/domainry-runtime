package openapi

func addOwnerOperationsReceiptOpenAPIContracts(paths map[string]any) {
	methods := map[string]string{
		"/operations/scheduler/definitions/{definitionID}/reschedule": "post",
		"/operations/scheduler/definitions/{definitionID}/run":        "post",
		"/operations/scheduler/runs/{runID}/retry":                    "post",
		"/operations/scheduler/runs/{runID}/cancel":                   "post",
		"/operations/scheduler/dead-letters/{deadLetterID}/resolve":   "post",
		"/operations/scheduler/dead-letters/{deadLetterID}/requeue":   "post",
		"/operations/workflow/executions/{executionID}/retry":         "post",
		"/operations/workflow/executions/{executionID}/resolve":       "post",
		"/operations/workflow/processes/{processID}/retry":            "post",
		"/operations/workflow/processes/{processID}/resolve":          "post",
		"/operations/agent/tasks/{taskRunID}/retry":                   "post",
		"/operations/agent/tasks/{taskRunID}/cancel":                  "post",
		"/operations/agent/tasks/{taskRunID}/resolve":                 "post",
		"/operations/agent/tasks/{taskRunID}/reconcile":               "post",
		"/operations/lifecycle/cleanup/jobs/{jobID}/run":              "post",
		"/operations/idempotency/receipts/{owner}/{receiptID}/retry":  "post",
		"/operations/idempotency/receipts/{owner}/{receiptID}/reset":  "post",
	}
	for path, method := range methods {
		pathItem, ok := paths[path].(map[string]any)
		if !ok {
			continue
		}
		operation, ok := pathItem[method].(map[string]any)
		if !ok {
			continue
		}
		parameters, _ := operation["parameters"].([]map[string]any)
		parameters = appendOpenAPIHeaderParameter(parameters, "Idempotency-Key", "Stable owner operation key", true)
		parameters = appendOpenAPIHeaderParameter(parameters, "X-Operation-Reason", "Auditable operator reason", false)
		parameters = appendOpenAPIHeaderParameter(parameters, "X-Operation-Reference", "Incident, ticket, or change reference", false)
		operation["parameters"] = parameters
		responses, _ := operation["responses"].(map[string]any)
		for status, raw := range responses {
			if status == "default" {
				continue
			}
			response, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			response["headers"] = ownerOperationsReceiptResponseHeaders()
		}
	}
}

func appendOpenAPIHeaderParameter(parameters []map[string]any, name, description string, required bool) []map[string]any {
	for _, parameter := range parameters {
		if parameter["in"] == "header" && parameter["name"] == name {
			return parameters
		}
	}
	return append(parameters, openAPIHeaderParameter(name, description, required))
}

func openAPIHeaderParameter(name, description string, required bool) map[string]any {
	return map[string]any{"name": name, "in": "header", "required": required, "description": description, "schema": map[string]any{"type": "string"}}
}

func ownerOperationsReceiptResponseHeaders() map[string]any {
	return map[string]any{
		"Location":             map[string]any{"description": "Durable Operations receipt URL", "schema": map[string]any{"type": "string"}},
		"Operation-ID":         map[string]any{"description": "Durable operation ID", "schema": map[string]any{"type": "string"}},
		"Operation-Location":   map[string]any{"description": "Durable Operations receipt URL", "schema": map[string]any{"type": "string"}},
		"Idempotency-Replayed": map[string]any{"description": "True when the terminal receipt was replayed", "schema": map[string]any{"type": "string"}},
	}
}
