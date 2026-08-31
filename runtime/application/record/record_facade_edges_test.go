package record

import (
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordruntime "github.com/domainry/domainry-runtime/runtime/domain/record/runtime"
	recordservice "github.com/domainry/domainry-runtime/runtime/domain/record/service"
)

func recordFacadeAuthorizationService() *RecordApplicationService {
	return &RecordApplicationService{
		create:       NewRecordCreateApplicationService(RecordCreateDependencies{}),
		update:       NewRecordUpdateApplicationService(RecordUpdateDependencies{}),
		delete:       NewRecordDeleteApplicationService(RecordDeleteDependencies{}),
		restore:      NewRecordRestoreApplicationService(RecordRestoreDependencies{}),
		importer:     NewRecordImportApplicationService(RecordImportDependencies{}),
		exporter:     NewRecordExportApplicationService(RecordExportDependencies{}),
		dataExchange: NewRecordDataExchangeApplicationService(RecordDataExchangeDependencies{}),
	}
}

func TestRecordFacadeRejectsUnknownWorkspaceBeforeDependencies(t *testing.T) {
	service := recordFacadeAuthorizationService()
	principal := principalmodel.Principal{}
	tests := []struct {
		name string
		call func() error
	}{
		{name: "enqueue import", call: func() error {
			_, _, err := service.EnqueueImportJob(t.Context(), "customer", []byte("name\nAcme\n"), "key", principal)
			return err
		}},
		{name: "enqueue export", call: func() error {
			_, _, err := service.EnqueueExportJob(t.Context(), "customer", "key", RecordExportOptions{}, principal)
			return err
		}},
		{name: "preview import", call: func() error { _, err := service.PreviewImport(t.Context(), "customer", nil, principal); return err }},
		{name: "apply import", call: func() error { _, err := service.ApplyImport(t.Context(), "customer", nil, principal); return err }},
		{name: "apply idempotent import", call: func() error {
			_, _, err := service.ApplyImportIdempotent(t.Context(), "customer", nil, "key", principal)
			return err
		}},
		{name: "create", call: func() error { _, err := service.CreateRecord(t.Context(), "customer", nil, principal); return err }},
		{name: "create idempotent", call: func() error {
			_, err := service.CreateRecordIdempotent(t.Context(), "customer", nil, "key", principal)
			return err
		}},
		{name: "create idempotent result", call: func() error {
			_, _, err := service.CreateRecordIdempotentResult(t.Context(), "customer", nil, "key", principal)
			return err
		}},
		{name: "update", call: func() error {
			_, err := service.UpdateRecord(t.Context(), "customer", "customer-1", nil, principal)
			return err
		}},
		{name: "delete", call: func() error { return service.DeleteRecord(t.Context(), "customer", "customer-1", principal) }},
		{name: "delete expected", call: func() error {
			return service.DeleteRecordExpected(t.Context(), "customer", "customer-1", "version", principal)
		}},
		{name: "restore", call: func() error {
			_, err := service.RestoreRecord(t.Context(), "customer", "customer-1", principal)
			return err
		}},
		{name: "export", call: func() error { _, _, err := service.ExportRecords(t.Context(), "customer", principal); return err }},
		{name: "export options", call: func() error {
			_, _, err := service.ExportRecordsWithOptions(t.Context(), "customer", principal, RecordExportOptions{})
			return err
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.call()
			if apperror.CodeOf(err) != "backend.workspace_scope_required" {
				t.Fatalf("err=%v code=%q", err, apperror.CodeOf(err))
			}
		})
	}
}

func TestRecordFacadeDataExchangeWorkerWithoutBindingStops(t *testing.T) {
	service := recordFacadeAuthorizationService()
	select {
	case <-service.StartDataExchangeWorker(t.Context(), 0, 0):
	default:
		t.Fatal("worker without Data Exchange must already be stopped")
	}
}

func TestRebuildOwnerDepartmentPathsRejectsBlankWorkspace(t *testing.T) {
	service := &RecordApplicationService{}
	if count, err := service.RebuildOwnerDepartmentPaths(t.Context(), " ", []identitysdk.WorkforceEntry{{IdentityUserID: "user-1"}}); count != 0 || apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("count=%d err=%v", count, err)
	}
}

func TestCreateRecordIdempotentResultReportsAcquireAndReplay(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	queryPolicy := recordservice.NewRecordQueryPolicyDomainService(recordservice.RecordQueryPolicyDependencies{
		Objects: func() []definitionmodel.ObjectSchema { return []definitionmodel.ObjectSchema{object} },
	})
	executions := &createExecutionProbe{}
	runtime := recordruntime.NewRecordMutationExecutionRuntime(executions)
	create := NewRecordCreateApplicationService(RecordCreateDependencies{
		Repository:       &createRepositoryProbe{},
		ObjectForAction:  queryPolicy.ObjectForAction,
		CanWrite:         func(principalmodel.Principal, definitionmodel.ObjectSchema, map[string]any) bool { return true },
		NewRecordID:      func(string) string { return "customer-1" },
		ExecutionRuntime: runtime,
	})
	service := &RecordApplicationService{queryPolicy: queryPolicy, create: create, recordMutationExecution: runtime}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}, RequestID: "request-a"}, accessfixture.Bundle{Permissions: []string{"workspace.admin", "*"}})

	first, replayed, err := service.CreateRecordIdempotentResult(t.Context(), object.Key, map[string]any{"name": "Acme"}, "create-key", principal)
	if err != nil || replayed || first.ID != "customer-1" || executions.commitCalls != 1 {
		t.Fatalf("first=%#v replayed=%v commits=%d err=%v", first, replayed, executions.commitCalls, err)
	}
	second, replayed, err := service.CreateRecordIdempotentResult(t.Context(), object.Key, map[string]any{"name": "Acme"}, "create-key", principal)
	if err != nil || !replayed || second.ID != first.ID || executions.commitCalls != 1 {
		t.Fatalf("second=%#v replayed=%v commits=%d err=%v", second, replayed, executions.commitCalls, err)
	}
}

func TestCreateRecordIdempotentResultRuntimeUnavailableAndBeginFailure(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	queryPolicy := recordservice.NewRecordQueryPolicyDomainService(recordservice.RecordQueryPolicyDependencies{
		Objects: func() []definitionmodel.ObjectSchema { return []definitionmodel.ObjectSchema{object} },
	})
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin", "*"}})
	service := &RecordApplicationService{queryPolicy: queryPolicy}
	if _, _, err := service.CreateRecordIdempotentResult(t.Context(), object.Key, map[string]any{"name": "Acme"}, "key", principal); apperror.CodeOf(err) != "backend.idempotency.receipt_unavailable" {
		t.Fatalf("missing runtime err=%v", err)
	}
	service.recordMutationExecution = recordruntime.NewRecordMutationExecutionRuntime(&createExecutionProbe{})
	if _, _, err := service.CreateRecordIdempotentResult(t.Context(), object.Key, map[string]any{"name": "Acme"}, " ", principal); apperror.CodeOf(err) != "backend.idempotency.key_required" {
		t.Fatalf("begin err=%v", err)
	}
}
