package service

import (
	"context"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

type scopeOwnerWorkforceDirectory struct {
	identityDirectoryNoop
	entries []identitysdk.WorkforceEntry
}

func (d scopeOwnerWorkforceDirectory) ListWorkforce(context.Context, identitysdk.DirectoryQuery) ([]identitysdk.WorkforceEntry, error) {
	return d.entries, nil
}

func scopeOwnerObject() definitionmodel.ObjectSchema {
	return definitionmodel.ObjectSchema{Key: "service_request", Fields: []definitionmodel.FieldSchema{
		{Key: "assignee_id", Type: "user", Config: map[string]any{"scope_owner": true}},
		{Key: "owner_department_id", Type: "text", Config: map[string]any{}},
		{Key: "owner_department_path", Type: "text", Config: map[string]any{}},
	}}
}

func TestRecordScopeOwnerFactDerivationUsesScopeOwnersWorkforceDepartment(t *testing.T) {
	service := NewRecordScopeOwnerFactDerivationDomainService(RecordScopeOwnerFactDerivationDependencies{WorkforceDirectory: scopeOwnerWorkforceDirectory{entries: []identitysdk.WorkforceEntry{{
		IdentityUserID: "technician", OrganizationUnitID: "field-ops", OrganizationPath: "/operations/field-ops",
	}}}})
	data := map[string]any{"assignee_id": "technician", "owner_department_id": "spoof", "owner_department_path": "/spoof"}
	if err := service.Apply(t.Context(), "workspace-primary", scopeOwnerObject(), data, "request-1"); err != nil {
		t.Fatal(err)
	}
	if data["owner_department_id"] != "field-ops" || data["owner_department_path"] != "/operations/field-ops" {
		t.Fatalf("derived owner department=%#v", data)
	}
}

func TestRecordScopeOwnerFactDerivationRejectsUnresolvedScopeOwner(t *testing.T) {
	service := NewRecordScopeOwnerFactDerivationDomainService(RecordScopeOwnerFactDerivationDependencies{WorkforceDirectory: scopeOwnerWorkforceDirectory{}})
	err := service.Apply(t.Context(), "workspace-primary", scopeOwnerObject(), map[string]any{"assignee_id": "missing"}, "request-1")
	if apperror.CodeOf(err) != "backend.record.scope_owner_department_unresolved" {
		t.Fatalf("unresolved owner error=%v", err)
	}
}
