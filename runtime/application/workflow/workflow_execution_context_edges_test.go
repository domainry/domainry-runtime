package workflow

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func TestWorkflowExecutionContextPrincipalAndRendering(t *testing.T) {
	worker := WorkflowWorkerPrincipal()
	if !worker.Known || worker.UserID != "workflow:worker" || !worker.SystemScope.Valid() || len(worker.SystemCapabilities) != 0 {
		t.Fatalf("worker=%+v", worker)
	}
	bundle := identitysdk.AccessBundle{}
	resolvedPrincipal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "workflow:flow", WorkspaceID: "workspace", AccessBundle: &bundle}}, accessfixture.Bundle{Key: "configured"})
	service := &WorkflowApplicationService{principals: &workflowPrincipalResolverTestStub{resolution: workflowPrincipalResolution(resolvedPrincipal)}}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "user", WorkspaceID: "workspace"}, RequestID: "request"}, accessfixture.Bundle{Key: "operator"})
	resolved, err := service.workflowPrincipal(t.Context(), definitionmodel.WorkflowSchema{Key: "flow", RunAs: "configured"}, principal)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.RoleKey != "configured" || resolved.WorkspaceID != "workspace" {
		t.Fatalf("resolved=%+v", resolved)
	}

	payload := map[string]any{"record_id": "record", "name": "Ada", "count": 2, "approval_comment": "needs revision", "approval_decision": "rejected"}
	source := recordmodel.Record{ID: "source", Data: map[string]any{"name": "Grace"}}
	for name, testCase := range map[string]struct {
		value    any
		expected any
	}{
		"non string":     {value: 3, expected: 3},
		"payload id":     {value: "$payload.id", expected: "record"},
		"payload field":  {value: "$payload.name", expected: "Ada"},
		"source id":      {value: "$source.id", expected: "source"},
		"source field":   {value: "$source.name", expected: "Grace"},
		"principal user": {value: "$principal.user_id", expected: "user"},
		"principal role": {value: "$principal.role_key", expected: "operator"},
		"literal":        {value: "literal", expected: "literal"},
	} {
		t.Run(name, func(t *testing.T) {
			if actual := renderWorkflowValueWithSource(t.Context(), testCase.value, payload, source, principal); !reflect.DeepEqual(actual, testCase.expected) {
				t.Fatalf("actual=%v expected=%v", actual, testCase.expected)
			}
		})
	}
	for _, token := range []string{"$now", "$today"} {
		if value := renderWorkflowValue(t.Context(), token, payload, principal); strings.TrimSpace(value.(string)) == "" {
			t.Fatalf("token %s empty", token)
		}
	}
	template := renderWorkflowTemplateWithSource(t.Context(), "$payload.name/$source.id/$source.name/$principal.user_id/$principal.role_key/$today/$now", payload, source, principal)
	for _, value := range []string{"Ada", "source", "Grace", "user", "operator"} {
		if !strings.Contains(template, value) {
			t.Fatalf("template=%q missing=%q", template, value)
		}
	}
	if value := renderWorkflowTemplate(t.Context(), "$payload.count/$source.id", payload, principal); value != "2/$source.id" {
		t.Fatalf("template without source=%q", value)
	}
	if value := renderWorkflowValue(t.Context(), "hello $payload.name", payload, principal); value != "hello Ada" {
		t.Fatalf("rendered template=%v", value)
	}
	if value := renderWorkflowValue(t.Context(), "$workflow.name", payload, principal); value != "Ada" {
		t.Fatalf("workflow payload alias=%v", value)
	}
	if value := renderWorkflowValue(t.Context(), "hello $workflow.name", payload, principal); value != "hello Ada" {
		t.Fatalf("workflow payload template alias=%v", value)
	}
	if comment := renderWorkflowValue(t.Context(), "$workflow.approval_comment", payload, principal); comment != "needs revision" {
		t.Fatalf("post-approval comment=%v", comment)
	}
	if decision := renderWorkflowValue(t.Context(), "$workflow.approval_decision", payload, principal); decision != "rejected" {
		t.Fatalf("post-approval decision=%v", decision)
	}
	for _, template := range []string{"hello $source.name", "hello $principal.user_id", "at $now", "on $today"} {
		if value := renderWorkflowValueWithSource(t.Context(), template, payload, source, principal); value == template {
			t.Fatalf("template was not rendered: %s", template)
		}
	}
	if workflowRenderedString(t.Context(), nil, payload, principal) != "" || workflowRenderedString(t.Context(), " $payload.name ", payload, principal) != "Ada" ||
		workflowRenderedString(t.Context(), "$workflow.id", payload, principal) != "record" ||
		workflowRenderedString(t.Context(), "$record.id", payload, principal) != "record" ||
		workflowRenderedString(t.Context(), "$record.name", payload, principal) != "Ada" {
		t.Fatal("rendered string mismatch")
	}
	if value := renderWorkflowValue(t.Context(), "record=$record.id/$record.name", payload, principal); value != "record=record/Ada" {
		t.Fatalf("record template=%v", value)
	}
	payload["agent_output"] = map[string]any{"result_json": `{"score":90}`, "nested": map[string]any{"decision": "contact"}}
	if value := renderWorkflowValue(t.Context(), "$workflow.agent_output.result_json", payload, principal); value != `{"score":90}` {
		t.Fatalf("nested workflow result=%v", value)
	}
	if value := renderWorkflowValue(t.Context(), "$workflow.agent_output.nested.decision", payload, principal); value != "contact" {
		t.Fatalf("deep workflow result=%v", value)
	}
	if value := renderWorkflowValue(t.Context(), "$workflow.agent_output.missing", payload, principal); value != nil {
		t.Fatalf("missing nested workflow result=%v", value)
	}
	if workflowSimpleTemplateRefKey("literal", "$payload.") != "" || workflowSimpleTemplateRefKey("$payload.", "$payload.") != "" || workflowSimpleTemplateRefKey("$payload.bad-key", "$payload.") != "" || workflowSimpleTemplateRefKey("$payload.good_1", "$payload.") != "good_1" {
		t.Fatal("simple reference validation mismatch")
	}
	if workflowSimpleTemplateRefKey("$payload.A", "$payload.") != "A" || workflowSimpleTemplateRefKey("$payload.{", "$payload.") != "" {
		t.Fatal("letter boundary validation mismatch")
	}
	if data := renderWorkflowRecordData(t.Context(), map[string]any{" ": "ignored", "name": "$payload.name"}, payload, principal); !reflect.DeepEqual(data, map[string]any{"name": "Ada"}) {
		t.Fatalf("data=%v", data)
	}
	if data := renderWorkflowRecordDataWithSource(t.Context(), map[string]any{" ": "ignored", "name": "$source.name"}, payload, source, principal); !reflect.DeepEqual(data, map[string]any{"name": "Grace"}) {
		t.Fatalf("source data=%v", data)
	}
}

func TestWorkflowExecutionContextPayloadRetryAndAuditProjection(t *testing.T) {
	record := recordmodel.Record{ID: "record", Data: map[string]any{"name": "new", "count": 2}, CreatedAt: "created", UpdatedAt: "updated"}
	payload := WorkflowPayloadForRecord("order", record)
	if payload["object_key"] != "order" || payload["record_id"] != "record" {
		t.Fatalf("payload=%v", payload)
	}
	changed := WorkflowPayloadForRecordChange("order", record, map[string]any{"name": "old", "count": 2}, "updated")
	if changed["trigger_event"] != "updated" || len(changed["changed_fields"].([]string)) != 1 {
		t.Fatalf("changed payload=%v", changed)
	}

	object := definitionmodel.ObjectSchema{Key: "order"}
	reader := workflowRecordReaderEdgeStub{records: map[string]recordmodel.Record{"record": record}}
	service := &WorkflowApplicationService{schemaMap: func(context.Context) map[string]definitionmodel.ObjectSchema {
		return map[string]definitionmodel.ObjectSchema{"order": object}
	}, recordReader: reader}
	previous := workflowmodel.WorkflowExecution{ID: "previous", Status: "failed", ObjectKey: "order", RecordID: "record", LastError: "provider.timeout", Payload: map[string]any{
		"request_id": "request", "initiating_user_id": "user", "initiating_role_key": "role", "scheduled_at": "time", "empty": "",
	}}
	refreshed := service.workflowRetryPayload(t.Context(), "workspace", previous)
	if refreshed["name"] != "new" || refreshed["retry_source_execution_id"] != "previous" || refreshed["retry_source_last_error"] != "provider.timeout" || refreshed["request_id"] != "request" {
		t.Fatalf("refreshed=%v", refreshed)
	}
	withoutLastError := previous
	withoutLastError.LastError = ""
	if _, exists := service.workflowRetryPayload(t.Context(), "workspace", withoutLastError)["retry_source_last_error"]; exists {
		t.Fatal("empty last error projected")
	}
	for name, candidate := range map[string]workflowmodel.WorkflowExecution{
		"missing identity": {Payload: map[string]any{"value": "kept"}},
		"missing record":   {ObjectKey: "order", Payload: map[string]any{"value": "kept"}},
		"missing object":   {ObjectKey: "missing", RecordID: "record", Payload: map[string]any{"value": "kept"}},
	} {
		t.Run(name, func(t *testing.T) {
			if actual := service.workflowRetryPayload(t.Context(), "workspace", candidate); actual["value"] != "kept" {
				t.Fatalf("payload=%v", actual)
			}
		})
	}
	partial := previous
	partial.Payload = map[string]any{"request_id": "request", "scheduled_at": ""}
	if actual := service.workflowRetryPayload(t.Context(), "workspace", partial); actual["request_id"] != "request" {
		t.Fatalf("partial retry payload=%v", actual)
	}
	service.recordReader = workflowRecordReaderEdgeStub{errID: "record"}
	if actual := service.workflowRetryPayload(t.Context(), "workspace", previous); actual["request_id"] != "request" {
		t.Fatalf("failed read payload=%v", actual)
	}
	service.recordReader = workflowRecordReaderEdgeStub{records: map[string]recordmodel.Record{}}
	if actual := service.workflowRetryPayload(t.Context(), "workspace", previous); actual["request_id"] != "request" {
		t.Fatalf("missing record payload=%v", actual)
	}

	workflow := definitionmodel.WorkflowSchema{Key: "flow", RunAs: "service", ConditionContract: &definitionmodel.WorkflowConditionContract{Fields: map[string]any{"changed_fields": []any{"name"}}}}
	base := workflowBaseResult(workflow, changed, "updated", "workflow_graph")
	if base["workflow_key"] != "flow" || base["trigger_record_id"] != "record" || base["run_as"] != "service" {
		t.Fatalf("base=%v", base)
	}
	execution := workflowmodel.WorkflowExecution{
		ID: "execution", WorkflowKey: "flow", Name: "Flow", Status: "failed", Trigger: "updated", ObjectKey: "fallback-object", RecordID: "fallback-record",
		Payload: map[string]any{"object_key": "order", "record_id": "record", "initiating_user_id": "initiator", "initiating_role_key": "initiator-role"},
		Result:  map[string]any{"created_record_id": "created-record", "updated_items": []any{"item"}, "coupon_id": "", "ignored": "value"},
		Attempt: 2, MaxAttempts: 3, RunAs: "service", ActionType: "workflow_graph", IdempotencyKey: "key", NextRunAt: time.Now().UTC().Format(time.RFC3339), LastError: "timeout",
	}
	metadata := workflowExecutionAuditMetadata(execution, accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{UserID: "fallback-user"}}, accessfixture.Bundle{Key: "fallback-role"}))
	for _, key := range []string{"workflow_key", "trigger_object_key", "trigger_record_id", "next_run_at", "last_error", "created_record_id", "updated_items"} {
		if _, exists := metadata[key]; !exists {
			t.Fatalf("metadata missing %s: %v", key, metadata)
		}
	}
	if _, exists := metadata["coupon_id"]; exists {
		t.Fatal("empty result projected")
	}
	minimal := execution
	minimal.Payload = nil
	minimal.Result = nil
	minimal.NextRunAt = ""
	minimal.LastError = ""
	minimalMetadata := workflowExecutionAuditMetadata(minimal, accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{UserID: "fallback-user"}}, accessfixture.Bundle{Key: "fallback-role"}))
	if minimalMetadata["trigger_object_key"] != "fallback-object" || minimalMetadata["initiated_by"] != "fallback-user" {
		t.Fatalf("minimal metadata=%v", minimalMetadata)
	}
}
