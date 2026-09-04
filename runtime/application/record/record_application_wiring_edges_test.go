package record

import (
	"context"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	publicationmodel "github.com/domainry/domainry-runtime/runtime/domain/publication/model"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func TestNewRecordApplicationServiceWiresOptionalCallbacks(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "customer"}
	record := recordmodel.Record{ID: "customer-1", Data: map[string]any{"name": "Acme"}}
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}
	counts := map[string]int{}
	service := NewRecordApplicationService(RecordApplicationDependencies{
		Repository:  &recordQueryRepositoryProbe{},
		QueryPolicy: nil,
		Audit: func(context.Context, string, string, string, principalmodel.Principal, string, map[string]any, map[string]any, map[string]any) {
			counts["audit"]++
		},
		PrepareWorkflow: func(context.Context, string, recordmodel.Record, map[string]any, principalmodel.Principal, string) ([]workflowmodel.WorkflowExecution, error) {
			counts["prepare"]++
			return []workflowmodel.WorkflowExecution{{ID: "workflow-1"}}, nil
		},
		ExecuteWorkflow: func(context.Context, []workflowmodel.WorkflowExecution, principalmodel.Principal) {
			counts["execute"]++
		},
		ApplyStateMachineSelfEffects: func(context.Context, definitionmodel.ObjectSchema, map[string]any, map[string]any, string, principalmodel.Principal) (bool, error) {
			counts["self_effects"]++
			return true, nil
		},
		SchemaMap: func() map[string]definitionmodel.ObjectSchema {
			counts["schema"]++
			return map[string]definitionmodel.ObjectSchema{object.Key: object}
		},
		IdentityProfileExtensions: func() []profilebindingmodel.Binding {
			counts["extensions"]++
			return []profilebindingmodel.Binding{{ObjectKey: "employee_profile"}}
		},
		FindBeforeCreateReplay: func(context.Context, definitionmodel.ObjectSchema, map[string]any, principalmodel.Principal) (recordmodel.Record, bool, error) {
			counts["replay"]++
			return record, true, nil
		},
		RunBefore: func(context.Context, string, string, string, map[string]any, map[string]any, map[string]any, principalmodel.Principal) error {
			counts["before"]++
			return nil
		},
		AfterOutbox: func(string, string, map[string]any, recordmodel.Record, principalmodel.Principal) []publicationmodel.Message {
			counts["outbox"]++
			return []publicationmodel.Message{{ID: "outbox-1"}}
		},
		BuildAudit: func(context.Context, string, string, string, principalmodel.Principal, string, map[string]any, map[string]any, map[string]any) auditmodel.AuditEvent {
			counts["build_audit"]++
			return auditmodel.AuditEvent{Event: "record_event"}
		},
	})

	service.audit(t.Context(), "event", object.Key, record.ID, principal, "summary", nil, nil, nil)
	if intents, err := service.prepareWorkflow(t.Context(), object.Key, record, nil, principal, "trigger"); err != nil || len(intents) != 1 {
		t.Fatalf("intents=%#v err=%v", intents, err)
	}
	service.executeWorkflow(t.Context(), []workflowmodel.WorkflowExecution{{ID: "workflow-1"}}, principal)
	if len(service.schemaMap()) != 1 || len(service.identityProfileExtensions()) != 1 {
		t.Fatal("schema and extensions were not delegated")
	}

	if replay, found, err := service.create.dependencies.FindReplay(t.Context(), object, record.Data, principal); err != nil || !found || replay.ID != record.ID {
		t.Fatalf("replay=%#v found=%v err=%v", replay, found, err)
	}
	if err := service.create.dependencies.RunBefore(t.Context(), object.Key, "create", record.ID, nil, nil, record.Data, principal); err != nil {
		t.Fatal(err)
	}
	if outbox := service.create.dependencies.AfterOutbox(object.Key, "create", nil, record, principal); len(outbox) != 1 {
		t.Fatalf("outbox=%#v", outbox)
	}
	if _, err := service.create.dependencies.PrepareWorkflow(t.Context(), object.Key, record, nil, principal, "trigger"); err != nil {
		t.Fatal(err)
	}
	service.create.dependencies.ExecuteWorkflow(t.Context(), nil, principal)
	service.create.dependencies.Audit(t.Context(), "event", object.Key, record.ID, principal, "summary", nil, nil, nil)
	if audit := service.create.dependencies.BuildAudit(t.Context(), "event", object.Key, record.ID, principal, "summary", nil, nil, nil); audit.Event != "record_event" {
		t.Fatalf("audit=%#v", audit)
	}

	if changed, err := service.update.dependencies.ApplySelfEffects(t.Context(), object, nil, record.Data, record.ID, principal); err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if err := service.update.dependencies.RunBefore(t.Context(), object.Key, "update", record.ID, nil, nil, record.Data, principal); err != nil {
		t.Fatal(err)
	}
	if err := service.delete.dependencies.RunBefore(t.Context(), object.Key, "delete", record.ID, nil, nil, record.Data, principal); err != nil {
		t.Fatal(err)
	}
	if users, err := service.exporter.dependencies.ListIdentityUsers(t.Context()); err != nil || users != nil {
		t.Fatalf("users=%#v err=%v", users, err)
	}

	for _, key := range []string{"audit", "prepare", "execute", "self_effects", "schema", "extensions", "replay", "before", "outbox", "build_audit"} {
		if counts[key] == 0 {
			t.Fatalf("callback %q was not wired: %#v", key, counts)
		}
	}
}
