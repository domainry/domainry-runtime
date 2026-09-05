package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	connector "github.com/domainry/domainry-connector-sdk"
	"github.com/domainry/domainry-foundation/requestcontext"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	identitymodule "github.com/domainry/domainry-identity/module"
	reportmodule "github.com/domainry/domainry-report/module"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	workspaceprovisionmodel "github.com/domainry/domainry-runtime/runtime/domain/workspaceprovision/model"
	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/workspaceprovision"
	runtimehttp "github.com/domainry/domainry-runtime/runtime/transport/http"
)

func TestM1BaselineManifestResolvesAcceptanceDepartmentDuringFirstRuntimeStartup(t *testing.T) {
	manifest := m1BaselineReferenceManifest()
	cfg := bootstrapTestConfig(t)
	cfg.ManifestPath = filepath.Join(t.TempDir(), "m1-runtime-manifest.json")
	rawManifest, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.ManifestPath, rawManifest, 0o600); err != nil {
		t.Fatal(err)
	}

	store, err := PrepareProjectDatabase(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	handle := identitysdk.DatabaseHandle{
		Pool: store.DB(), Driver: store.Driver(), Schema: store.DatabaseSchema(), FilePath: cfg.DBPath,
		Migrations: store, ModuleMigrations: store,
	}
	identityFactory := identitymodule.NewFactory(identitymodule.Options{IdentityVersion: "test", DatabaseDriver: "sqlite", DatabasePath: cfg.DBPath})
	bootstrapBinding, err := identityFactory.OpenBootstrapWithDatabase(t.Context(), identitysdk.ApplicationKey(cfg.IdentityAudience), handle)
	if err != nil {
		t.Fatal(err)
	}
	if err := bootstrapBinding.BindBootstrapProjectRoleCatalog(t.Context(), RuntimeProjectRoleCatalog(manifest.Objects, manifest.Roles, "", cfg.IdentityAudience)); err != nil {
		t.Fatal(err)
	}
	organizations := []identitysdk.WorkspaceAcceptanceOrganization{
		{ID: "department-2", Code: "department-2", Name: "Department 2"},
		{ID: "department-1", Code: "department-1", Name: "Department 1"},
	}
	actors := []identitysdk.WorkspaceAcceptanceActor{
		{ID: "director", LoginID: "director@example.test", Name: "Director", RoleKey: "sales_director", InitialPassword: "ActorPassword1!"},
		{ID: "rep-1", LoginID: "rep-1@example.test", Name: "Rep 1", RoleKey: "sales_rep", OrganizationID: "department-1", ManagerUserID: "director", InitialPassword: "ActorPassword1!"},
		{ID: "rep-2", LoginID: "rep-2@example.test", Name: "Rep 2", RoleKey: "sales_rep", OrganizationID: "department-2", ManagerUserID: "director", InitialPassword: "ActorPassword1!"},
	}
	provisioned, err := workspaceprovision.NewTenantInitializationStore(store, bootstrapBinding, manifest).InitializeWithAcceptanceFixtures(
		t.Context(), workspaceprovisionmodel.Request{
			RequestID: "m1-baseline-reference", TenantCode: "m1-crm", TenantName: "M1 CRM",
			AdminLoginID: "admin@example.test", AdminName: "Admin", StoreConfiguration: map[string]any{},
		}, "BootstrapAdmin1!", organizations, actors,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := bootstrapBinding.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	cfg.IdentityWorkspaceID = provisioned.WorkspaceID
	cfg.NotificationTenantID = provisioned.WorkspaceID
	cfg.NotificationWorkspaceID = provisioned.WorkspaceID
	identityBinding, err := identityFactory.OpenWithDatabase(t.Context(), identitysdk.ApplicationRef{
		WorkspaceID: identitysdk.WorkspaceID(provisioned.WorkspaceID), ApplicationKey: identitysdk.ApplicationKey(cfg.IdentityAudience),
	}, handle)
	if err != nil {
		t.Fatal(err)
	}

	handlers := runtimeext.NewBusinessHandlerRegistry()
	handlers.Freeze()
	connectors := connector.NewRegistry()
	connectors.Freeze()
	candidates := []BusinessSeedReferenceCandidate{
		{WorkspaceID: provisioned.WorkspaceID, TargetObjectKey: "identity_user", RecordID: "admin", SourceKind: "runtime_initial_administrator"},
		{WorkspaceID: provisioned.WorkspaceID, TargetObjectKey: "identity_user", RecordID: "director", SourceKind: "runtime_acceptance_fixture"},
		{WorkspaceID: provisioned.WorkspaceID, TargetObjectKey: "identity_user", RecordID: "rep-1", SourceKind: "runtime_acceptance_fixture"},
		{WorkspaceID: provisioned.WorkspaceID, TargetObjectKey: "identity_user", RecordID: "rep-2", SourceKind: "runtime_acceptance_fixture"},
		{WorkspaceID: provisioned.WorkspaceID, TargetObjectKey: "identity_organization_unit", RecordID: "department-2", SourceKind: "runtime_acceptance_fixture"},
		{WorkspaceID: provisioned.WorkspaceID, TargetObjectKey: "identity_organization_unit", RecordID: "department-1", SourceKind: "runtime_acceptance_fixture"},
	}
	application := newWithExtensionsUsingAllFactoriesAndStore(
		t.Context(), cfg, handlers, connectors, runtimehttp.RuntimeReleaseIdentity{}, identityBinding,
		runtimeTestNotificationFactory(), nil, nil, runtimeTestDataExchangeFactory(), nil,
		runtimeTestIntegrationFactory(), reportmodule.NewFactory(), store,
		ProjectStartupOptions{BusinessSeedReferenceCandidates: candidates},
	)
	t.Cleanup(func() { _ = application.CloseContext(t.Context()) })

	leadObject := manifest.Objects[0]
	page, err := recordpersistence.NewRecordStore(store).ListRecords(
		requestcontext.WithWorkspaceID(t.Context(), provisioned.WorkspaceID), provisioned.WorkspaceID, leadObject,
		recordmodel.RecordListQuery{Page: 1, PageSize: 10, AuthorizationMode: recordmodel.RecordQueryAuthorizationUnrestricted},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Data["department_id"] != "department-1" || page.Items[0].Data["owner_id"] != "admin" {
		t.Fatalf("M1 baseline leads=%+v", page.Items)
	}
	organization, found, err := identityBinding.Projection().FindOrganizationUnit(t.Context(), identitysdk.OrganizationUnitLookup{
		Application: identitysdk.ApplicationScope{
			WorkspaceID: identitysdk.WorkspaceID(provisioned.WorkspaceID), ApplicationKey: identitysdk.ApplicationKey(cfg.IdentityAudience),
		},
		OrgID: "department-1",
	})
	if err != nil || !found || organization.ID != "department-1" {
		t.Fatalf("selected department is not owned by initialized workspace: organization=%+v found=%t err=%v", organization, found, err)
	}
}

func m1BaselineReferenceManifest() manifestmodel.ManifestSchema {
	return manifestmodel.ManifestSchema{
		SchemaVersion: manifestmodel.CurrentManifestSchemaVersion,
		TemplateID:    "domain_domainry_m1_crm_reference", Version: "0.1.0", Name: "M1 CRM Reference", DefaultLocale: "en-US",
		Objects: []definitionmodel.ObjectSchema{{
			Key: "lead", Name: "Lead", Config: map[string]any{"write_policy": "action_only"},
			Fields: []definitionmodel.FieldSchema{
				{Key: "company_name", Name: "Company Name", Type: "text", Required: true, Validation: definitionmodel.FieldValidation{MaxLength: 200}},
				{Key: "contact_name", Name: "Contact Name", Type: "text", Validation: definitionmodel.FieldValidation{MaxLength: 100}},
				{Key: "contact_phone", Name: "Contact Phone", Type: "phone"},
				{Key: "department_id", Name: "Department Id", Type: "relation", Required: true, Config: map[string]any{"cardinality": "many_to_one", "indexed": true, "on_delete": "restrict", "target": "identity_organization_unit"}, Validation: definitionmodel.FieldValidation{Target: "identity_organization_unit"}},
				{Key: "expected_amount", Name: "Expected Amount", Type: "currency", Config: map[string]any{"currency_code": "XXX", "precision": 18, "rounding_mode": "half_even", "scale": 2}},
				{Key: "owner_id", Name: "Owner Id", Type: "user", Required: true},
				{Key: "source", Name: "Source", Type: "select", Required: true, Validation: definitionmodel.FieldValidation{Options: []string{"website", "exhibition", "referral", "outbound"}}},
				{Key: "status", Name: "Status", Type: "select", Required: true, DefaultValue: "new", Validation: definitionmodel.FieldValidation{Options: []string{"new", "contacted", "qualified", "converted", "lost"}}},
				{Key: "status_changed_at", Name: "Status Changed At", Type: "datetime", Required: true},
			},
		}},
		Roles: []manifestmodel.RoleSchema{
			{Key: "sales_director", Name: "Sales Director", Audience: "any", ProvisionToWorkspaces: true, Permissions: []manifestmodel.RolePermission{{PermissionKey: "lead.read", DataScope: identitysdk.DataScopeAll}}},
			{Key: "sales_rep", Name: "Sales Rep", Audience: "any", ProvisionToWorkspaces: true, Permissions: []manifestmodel.RolePermission{{PermissionKey: "lead.read", DataScope: identitysdk.DataScopeOrg}}},
		},
	}
}
