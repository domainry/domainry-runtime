package record

import (
	"context"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	recordservice "github.com/domainry/domainry-runtime/runtime/domain/record/service"
)

type recordQueryRepositoryProbe struct {
	recordrepository.RecordRepository
	objects map[string]definitionmodel.ObjectSchema
}

func (r *recordQueryRepositoryProbe) GetRecord(_ context.Context, _ string, object definitionmodel.ObjectSchema, recordID string) (recordmodel.Record, bool, error) {
	return recordmodel.Record{ID: recordID, Data: map[string]any{"name": object.Key}}, true, nil
}

func (r *recordQueryRepositoryProbe) ListRecords(_ context.Context, _ string, object definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	record := recordmodel.Record{ID: object.Key + "-1", Data: map[string]any{"name": object.Key}}
	if object.Key == "line_item" {
		record.Data["customer_id"] = "customer-1"
	}
	if relationID, ok := query.Filters["customer_id"]; ok {
		record.Data["customer_id"] = relationID
	}
	return recordmodel.RecordPageResult{Items: []recordmodel.Record{record}, Total: 1}, nil
}

func TestRecordQueryEntrypointsRejectUnknownPrincipalBeforeDelegation(t *testing.T) {
	service := &RecordApplicationService{}
	principal := principalmodel.Principal{}

	tests := []struct {
		name string
		call func() error
	}{
		{name: "object for action", call: func() error {
			_, err := service.ObjectForAction(principal, "orders", "view")
			return err
		}},
		{name: "list records", call: func() error {
			_, err := service.ListRecords(t.Context(), "orders", recordmodel.RecordListQuery{}, principal)
			return err
		}},
		{name: "get record", call: func() error {
			_, err := service.GetRecord(t.Context(), "orders", "order-1", principal)
			return err
		}},
		{name: "record references", call: func() error {
			_, err := service.RecordReferences(t.Context(), "orders", "order-1", principal)
			return err
		}},
		{name: "related records", call: func() error {
			_, err := service.RelatedRecords(t.Context(), "orders", "order-1", "items", recordservice.RecordRelatedRecordsRequest{}, principal)
			return err
		}},
		{name: "identity profile references", call: func() error {
			_, err := service.IdentityProfileReferences(t.Context(), "user-1", principal)
			return err
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.call()
			if err == nil || apperror.CodeOf(err) != "backend.workspace_scope_required" {
				t.Fatalf("err=%v code=%q", err, apperror.CodeOf(err))
			}
		})
	}

	if service.CanAccessRecord(principal, definitionmodel.ObjectSchema{}, recordmodel.Record{}) {
		t.Fatal("unknown principal must not access records")
	}
}

func TestRecordQueryEntrypointsDelegateForAuthorizedWorkspace(t *testing.T) {
	customer := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	lineItem := definitionmodel.ObjectSchema{Key: "line_item", Fields: []definitionmodel.FieldSchema{{Key: "customer_id", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "customer"}}}}
	profile := definitionmodel.ObjectSchema{Key: "employee_profile", Fields: []definitionmodel.FieldSchema{{Key: "identity_user", Type: "relation"}}}
	objects := map[string]definitionmodel.ObjectSchema{customer.Key: customer, lineItem.Key: lineItem, profile.Key: profile}
	repository := &recordQueryRepositoryProbe{objects: objects}
	queryPolicy := recordservice.NewRecordQueryPolicyDomainService(recordservice.RecordQueryPolicyDependencies{
		Objects: func() []definitionmodel.ObjectSchema {
			return []definitionmodel.ObjectSchema{customer, lineItem, profile}
		},
	})
	service := NewRecordApplicationService(RecordApplicationDependencies{
		Repository:  repository,
		QueryPolicy: queryPolicy,
		SchemaMap:   func() map[string]definitionmodel.ObjectSchema { return objects },
		IdentityProfileExtensions: func() []profilebindingmodel.Binding {
			return []profilebindingmodel.Binding{{ObjectKey: profile.Key, IdentityRelationField: "identity_user"}}
		},
	})
	principal := recordFullAccessPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "admin"}})

	if object, err := service.ObjectForAction(principal, customer.Key, "read"); err != nil || object.Key != customer.Key {
		t.Fatalf("object=%#v err=%v", object, err)
	}
	if page, err := service.ListRecords(t.Context(), customer.Key, recordmodel.RecordListQuery{}, principal); err != nil || len(page.Items) != 1 {
		t.Fatalf("page=%#v err=%v", page, err)
	}
	if query := service.NormalizeListQuery(customer, recordmodel.RecordListQuery{Page: -1}, principal); query.Page != 1 {
		t.Fatalf("normalized query=%#v", query)
	}
	if record, err := service.GetRecord(t.Context(), customer.Key, "customer-1", principal); err != nil || record.ID != "customer-1" {
		t.Fatalf("record=%#v err=%v", record, err)
	}
	if summary, err := service.RecordReferences(t.Context(), customer.Key, "customer-1", principal); err != nil || summary.Total != 1 {
		t.Fatalf("summary=%#v err=%v", summary, err)
	}
	if page, err := service.RelatedRecords(t.Context(), customer.Key, "customer-1", lineItem.Key, recordservice.RecordRelatedRecordsRequest{}, principal); err != nil || len(page.Items) != 1 {
		t.Fatalf("related page=%#v err=%v", page, err)
	}
	if references, err := service.IdentityProfileReferences(t.Context(), "user-1", principal); err != nil || len(references) != 1 || references[0].RecordCount != 1 {
		t.Fatalf("identity profile references=%#v err=%v", references, err)
	}
	if !service.CanAccessRecord(principal, customer, recordmodel.Record{ID: "customer-1"}) {
		t.Fatal("workspace administrator should access the record")
	}
}

func TestRecordCommandAuthorizationRejectsMissingWorkspace(t *testing.T) {
	if err := recordAuthorizeCommand(principalmodel.Principal{}); err == nil || apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("unknown principal err=%v code=%q", err, apperror.CodeOf(err))
	}
	err := recordAuthorizeCommand(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}})
	if err == nil || apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("err=%v code=%q", err, apperror.CodeOf(err))
	}
	if err := recordAuthorizeCommand(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}); err != nil {
		t.Fatalf("valid workspace rejected: %v", err)
	}
}

func TestRecordFacadeDelegatesAuthorizedWorkspaceToOwnedServices(t *testing.T) {
	customer := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	objects := map[string]definitionmodel.ObjectSchema{customer.Key: customer}
	repository := &recordQueryRepositoryProbe{objects: objects}
	queryPolicy := recordservice.NewRecordQueryPolicyDomainService(recordservice.RecordQueryPolicyDependencies{
		Objects: func() []definitionmodel.ObjectSchema { return []definitionmodel.ObjectSchema{customer} },
	})
	service := NewRecordApplicationService(RecordApplicationDependencies{
		Repository:  repository,
		QueryPolicy: queryPolicy,
		SchemaMap:   func() map[string]definitionmodel.ObjectSchema { return objects },
	})
	principal := recordFullAccessPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "admin"}})

	if _, err := service.PreviewImport(t.Context(), customer.Key, nil, principal); apperror.CodeOf(err) != "backend.import.header_required" {
		t.Fatalf("preview err=%v", err)
	}
	if _, err := service.ApplyImport(t.Context(), customer.Key, nil, principal); apperror.CodeOf(err) != "backend.import.header_required" {
		t.Fatalf("apply err=%v", err)
	}
	if _, _, err := service.ApplyImportIdempotent(t.Context(), customer.Key, nil, "operation-1", principal); apperror.CodeOf(err) != "backend.import.header_required" {
		t.Fatalf("idempotent apply err=%v", err)
	}

	for _, call := range []struct {
		name string
		run  func() error
	}{
		{name: "create", run: func() error { _, err := service.CreateRecord(t.Context(), "missing", nil, principal); return err }},
		{name: "create idempotent", run: func() error {
			_, err := service.CreateRecordIdempotent(t.Context(), "missing", nil, "key", principal)
			return err
		}},
		{name: "create idempotent result", run: func() error {
			_, _, err := service.CreateRecordIdempotentResult(t.Context(), "missing", nil, "key", principal)
			return err
		}},
		{name: "update", run: func() error {
			_, err := service.UpdateRecord(t.Context(), "missing", "record-1", nil, principal)
			return err
		}},
		{name: "delete", run: func() error { return service.DeleteRecord(t.Context(), "missing", "record-1", principal) }},
		{name: "delete expected", run: func() error {
			return service.DeleteRecordExpected(t.Context(), "missing", "record-1", "revision", principal)
		}},
		{name: "restore", run: func() error {
			_, err := service.RestoreRecord(t.Context(), "missing", "record-1", principal)
			return err
		}},
	} {
		t.Run(call.name, func(t *testing.T) {
			if err := call.run(); apperror.CodeOf(err) != "backend.object.not_found" {
				t.Fatalf("err=%v code=%q", err, apperror.CodeOf(err))
			}
		})
	}

	if content, filename, err := service.ExportRecords(t.Context(), customer.Key, principal); err != nil || filename != "customer.csv" || len(content) == 0 {
		t.Fatalf("filename=%q content=%q err=%v", filename, content, err)
	}
	if content, filename, err := service.ExportRecordsWithOptions(t.Context(), customer.Key, principal, RecordExportOptions{Fields: []string{"name"}}); err != nil || filename != "customer.csv" || len(content) == 0 {
		t.Fatalf("filename=%q content=%q err=%v", filename, content, err)
	}
}
