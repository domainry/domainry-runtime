package service

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestActionPatchUsesOnlyDeclaredInput(t *testing.T) {
	object := definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{
		{Key: "status", Type: "select"},
		{Key: "stage", Type: "select", Config: map[string]any{"options": []string{"new", "qualified", "won"}}},
		{Key: "name", Type: "text"},
	}}
	record := recordmodel.Record{ID: "record-1", Data: map[string]any{"stage": "new", "name": "Current"}}
	principal := principalmodel.Principal{Principal: identitysdk.Principal{UserID: "user-1"}}

	patch := ActionPatch(definitionmodel.ActionSchema{}, object, record, map[string]any{
		"name": "Updated", "status": "draft", "unknown": true,
		" ": true, "expected_version": 1, "expectedVersion": 1, "version": 1,
	}, principal)
	if patch["name"] != "Updated" || patch["status"] != "draft" || len(patch) != 2 {
		t.Fatalf("input patch=%v", patch)
	}
	for _, key := range []string{"object.mark_won", "object.complete", "object.approve", "object.advance_stage"} {
		if got := ActionPatch(definitionmodel.ActionSchema{Key: key}, object, record, nil, principal); len(got) != 0 {
			t.Fatalf("undeclared business action %s inferred patch=%v", key, got)
		}
	}
}

func TestActionValueRenderingAndDocumentAnswers(t *testing.T) {
	now := time.Now().UTC()
	record := recordmodel.Record{
		ID: "record-1", CreatedAt: now.Add(-time.Hour).Format(time.RFC3339), UpdatedAt: now.Format(time.RFC3339),
		Data: map[string]any{"name": "Record", "version_group_id": " group-1 ", "version": json.Number("4"), "title": "Policy", "file_name": "policy.pdf", "status": "draft"},
	}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{UserID: "user-1"}}, accessfixture.Bundle{Key: "reviewer"})
	input := map[string]any{"name": "Input", "question": "What are the risks?"}
	tests := []struct {
		value any
		want  any
	}{
		{value: 12, want: 12},
		{value: "$input.name", want: "Input"},
		{value: "$input.missing", want: nil},
		{value: "$record.id", want: "record-1"},
		{value: "$record.created_at", want: record.CreatedAt},
		{value: "$record.updated_at", want: record.UpdatedAt},
		{value: "$record.name", want: "Record"},
		{value: "$record.version_group_id_or_id", want: "group-1"},
		{value: "$record.version_plus_one", want: 5},
		{value: "$principal.user_id", want: "user-1"},
		{value: "$principal.role_key", want: "reviewer"},
		{value: "literal", want: "literal"},
	}
	for _, test := range tests {
		if got := ActionRenderValueWithInput(test.value, record, input, principal); got != test.want {
			t.Fatalf("render(%v)=%v want %v", test.value, got, test.want)
		}
	}
	if got := ActionRenderValueWithInput("$input.name", record, nil, principal); got != nil {
		t.Fatalf("nil input rendered=%v", got)
	}
	for _, token := range []string{"$now", "$today"} {
		if value, ok := ActionRenderValueWithInput(token, record, input, principal).(string); !ok || value == "" {
			t.Fatalf("time token %s=%v", token, value)
		}
	}
	record.Data["version_group_id"] = ""
	record.Data["version"] = "invalid"
	if ActionRenderValueWithInput("$record.version_group_id_or_id", record, input, principal) != record.ID || ActionRenderValueWithInput("$record.version_plus_one", record, input, principal) != 2 {
		t.Fatal("record fallback rendering failed")
	}

	source := recordmodel.Record{ID: "source-1", CreatedAt: now.Add(-2 * time.Hour).Format(time.RFC3339), UpdatedAt: now.Add(-time.Hour).Format(time.RFC3339), Data: map[string]any{"name": "Source"}}
	for value, want := range map[string]any{"$source.id": source.ID, "$source.created_at": source.CreatedAt, "$source.updated_at": source.UpdatedAt, "$source.name": "Source", "$record.id": record.ID} {
		if got := ActionRenderRelatedValue(value, record, source, principal); got != want {
			t.Fatalf("related(%s)=%v want %v", value, got, want)
		}
	}
	if ActionRenderRelatedValue(7, record, source, principal) != 7 {
		t.Fatal("non-string related value changed")
	}
	rendered := ActionRenderRecordData(map[string]any{" id ": "$record.id", " ": "ignored"}, record, principal)
	if rendered["id"] != record.ID || len(rendered) != 1 {
		t.Fatalf("rendered record data=%v", rendered)
	}
	renderedWithInput := ActionRenderRecordDataWithInput(map[string]any{" name ": "$input.name", " ": "ignored"}, record, input, principal)
	if renderedWithInput["name"] != "Input" || len(renderedWithInput) != 1 {
		t.Fatalf("rendered record data with input=%v", renderedWithInput)
	}
	renderedRelated := ActionRenderRelatedRecordData(map[string]any{" source ": "$source.id", " ": "ignored"}, record, source, principal)
	if renderedRelated["source"] != source.ID || len(renderedRelated) != 1 {
		t.Fatalf("rendered related record data=%v", renderedRelated)
	}
	if summary := ActionRenderValueWithInput("$document.ai_summary", record, input, principal); !strings.Contains(summary.(string), "title Policy") {
		t.Fatalf("rendered AI summary=%q", summary)
	}
	if answer := ActionRenderValueWithInput("$document.ai_answer", record, input, principal); !strings.Contains(answer.(string), "What are the risks?") {
		t.Fatalf("rendered AI answer=%q", answer)
	}
	if summary := ActionDocumentAISummary(record); !strings.Contains(summary, "title Policy") || !strings.Contains(summary, "file policy.pdf") {
		t.Fatalf("summary=%q", summary)
	}
	if answer := ActionDocumentAIAnswer(record, input); !strings.Contains(answer, "What are the risks?") {
		t.Fatalf("answer=%q", answer)
	}
	if answer := ActionDocumentAIAnswer(record, nil); !strings.Contains(answer, "Summarize the key points") {
		t.Fatalf("default answer=%q", answer)
	}
	if summary := ActionDocumentAISummary(recordmodel.Record{}); !strings.Contains(summary, "no readable metadata") {
		t.Fatalf("empty summary=%q", summary)
	}
	if cleanProjectionValue(nil) != "" || cleanProjectionValue(" value ") != "value" {
		t.Fatal("projection cleanup failed")
	}
}

func TestIntegerValueAndExplicitEmptyPatchEdges(t *testing.T) {
	for value, want := range map[any]int{int(1): 1, int64(2): 2, float64(3.8): 3, json.Number("4"): 4, " 5 ": 5} {
		if got, ok := integerValue(value); !ok || got != want {
			t.Fatalf("integer(%v)=%d ok=%v", value, got, ok)
		}
	}
	for _, value := range []any{nil, true, "bad", json.Number("4.5")} {
		if _, ok := integerValue(value); ok {
			t.Fatalf("invalid integer %v accepted", value)
		}
	}
	object := definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{{Key: "name"}, {Key: "status"}}}
	normalized := map[string]any{"name": "kept", "status": "active"}
	if got := ActionPreserveExplicitEmptyPatch(object, nil, normalized); got["name"] != "kept" {
		t.Fatalf("nil patch=%v", got)
	}
	got := ActionPreserveExplicitEmptyPatch(object, map[string]any{" name ": nil, "status": "active", "unknown": nil}, normalized)
	if got["name"] != "" || got["status"] != "active" || got["unknown"] != nil {
		t.Fatalf("preserved patch=%v", got)
	}
}
