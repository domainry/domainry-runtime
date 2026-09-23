package integrationtest

import (
	"context"
	"sort"
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"

	. "github.com/domainry/domainry-runtime/runtime/bootstrap/composition"

	apperror "github.com/domainry/domainry-foundation/apperror"

	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"

	actionpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/action"

	auditpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/auditmodule"

	automationpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/automation"

	appschemapersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/appschema"
	publicationhandoffpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/publicationhandoff"

	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"

	workflowpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/workflow"
	metadatamodulefixture "github.com/domainry/domainry-runtime/testsupport/metadatamodulefixture"
)

// integrationTestIdentityProjection models the external Identity projection at
// the Runtime boundary. Workflow tests must not recreate Plane's retired
// Identity owner or persist Identity state in the Runtime database.
type integrationTestIdentityProjection struct {
	users             map[string]identitysdk.User
	organizationUnits map[string]identitysdk.OrganizationUnit
}

func newIntegrationTestIdentityProjection() *integrationTestIdentityProjection {
	return &integrationTestIdentityProjection{
		users:             map[string]identitysdk.User{},
		organizationUnits: map[string]identitysdk.OrganizationUnit{},
	}
}

func (d *integrationTestIdentityProjection) upsertUser(user identitysdk.User) {
	d.users[user.ID] = user
}

func (d *integrationTestIdentityProjection) FindUser(_ context.Context, lookup identitysdk.UserLookup) (identitysdk.User, bool, error) {
	user, found := d.users[string(lookup.UserID)]
	return user, found, nil
}

func (d *integrationTestIdentityProjection) FindOrganizationUnit(_ context.Context, lookup identitysdk.OrganizationUnitLookup) (identitysdk.OrganizationUnit, bool, error) {
	organizationUnit, found := d.organizationUnits[lookup.OrgID]
	return organizationUnit, found, nil
}

func (d *integrationTestIdentityProjection) ListUsers(context.Context, identitysdk.ProjectionQuery) ([]identitysdk.User, error) {
	values := make([]identitysdk.User, 0, len(d.users))
	for _, user := range d.users {
		values = append(values, user)
	}
	sort.Slice(values, func(i, j int) bool { return values[i].ID < values[j].ID })
	return values, nil
}

func (d *integrationTestIdentityProjection) ListRoles(context.Context, identitysdk.ProjectionQuery) ([]identitysdk.Role, error) {
	return nil, nil
}

func (d *integrationTestIdentityProjection) ListUserRoleAssignments(context.Context, identitysdk.UserRoleAssignmentQuery) ([]identitysdk.UserRoleAssignment, error) {
	return nil, nil
}

func objectActionTestDependencies(ctx context.Context, store *persistence.RuntimeStore) RuntimeServicesDependencies {
	if store == nil {
		return RuntimeServicesDependencies{}
	}
	metadatamodulefixture.EnsureBinding(ctx, store)
	return RuntimeServicesDependencies{
		Records:                      recordpersistence.NewRecordStore(store),
		Audit:                        auditpersistence.NewAuditStoreFromRuntimeStore(ctx, store),
		IntegrationPublication:       publicationhandoffpersistence.NewPublicationStore(store),
		IntegrationPublicationWorker: publicationhandoffpersistence.NewWorkerStore(store),
		ApplicationSchema:            appschemapersistence.NewApplicationSchemaStore(store),
		WorkflowWorker:               workflowpersistence.NewWorkflowWorkerStore(store),
		WorkflowDefinitions:          workflowpersistence.NewWorkflowDefinitionStore(store),
		WorkflowProcesses:            workflowpersistence.NewWorkflowProcessStore(store),
		WorkflowDecisions:            workflowpersistence.NewWorkflowDecisionStore(store),
		AutomationWorker:             automationpersistence.NewAutomationWorkerStore(store),
		AutomationExecutions:         automationpersistence.NewAutomationExecutionStore(store),
		ActionExecutions:             actionpersistence.NewActionBusinessExecutionStore(store),
	}
}

func recordLegacyStore(store *persistence.RuntimeStore) recordpersistence.RecordStore {
	return recordpersistence.NewRecordStore(store)
}

func serviceErrorCode(err error) string {
	return apperror.CodeOf(err)
}

func mapFromAny(value any) map[string]any {
	mapped, _ := value.(map[string]any)
	return mapped
}

func auditStore(ctx context.Context, store *persistence.RuntimeStore) *auditpersistence.AuditStore {
	return auditpersistence.NewAuditStoreFromRuntimeStore(ctx, store)
}

func workflowProcessStore(store *persistence.RuntimeStore) workflowpersistence.WorkflowProcessStore {
	return workflowpersistence.NewWorkflowProcessStore(store)
}

func workflowWorkerStore(store *persistence.RuntimeStore) workflowpersistence.WorkflowWorkerStore {
	return workflowpersistence.NewWorkflowWorkerStore(store)
}

func workflowDefinitionStore(store *persistence.RuntimeStore) workflowpersistence.WorkflowDefinitionStore {
	return workflowpersistence.NewWorkflowDefinitionStore(store)
}

func listWorkflowProcesses(t *testing.T, store *persistence.RuntimeStore, status, objectKey, recordID string, limit int) ([]workflowmodel.WorkflowProcessInstance, error) {
	t.Helper()
	return workflowProcessStore(store).ListProcesses(t.Context(), "workspace-primary", workflowmodel.WorkflowProcessFilter{Status: status, ObjectKey: objectKey, RecordID: recordID, Limit: limit})
}

func mustUpsertIdentityUser(t *testing.T, projection *integrationTestIdentityProjection, user identitysdk.User) {
	t.Helper()
	if strings.TrimSpace(user.Status) == "" {
		user.Status = identitysdk.UserStatusActive
	}
	projection.upsertUser(user)
}
