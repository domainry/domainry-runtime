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

	auditpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/audit"

	automationpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/automation"

	changeplanpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/changeplan"

	integrationpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/integration"
	metadatapersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/metadata"

	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"

	workflowpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/workflow"
)

// integrationTestIdentityDirectory models the external Identity directory at
// the Runtime boundary. Workflow tests must not recreate Plane's retired
// Identity owner or persist Identity state in the Runtime database.
type integrationTestIdentityDirectory struct {
	users       map[string]identitysdk.User
	departments map[string]identitysdk.Department
	workforce   map[string]identitysdk.WorkforceEntry
}

func newIntegrationTestIdentityDirectory() *integrationTestIdentityDirectory {
	return &integrationTestIdentityDirectory{
		users:       map[string]identitysdk.User{},
		departments: map[string]identitysdk.Department{},
		workforce:   map[string]identitysdk.WorkforceEntry{},
	}
}

func (d *integrationTestIdentityDirectory) upsertUser(user identitysdk.User) {
	d.users[user.ID] = user
}

func (d *integrationTestIdentityDirectory) setManager(managerUserID, employeeUserID string) {
	d.workforce[managerUserID] = identitysdk.WorkforceEntry{WorkforceProfileID: managerUserID + "_workforce", IdentityUserID: managerUserID, OrganizationUnitID: "company"}
	d.workforce[employeeUserID] = identitysdk.WorkforceEntry{WorkforceProfileID: employeeUserID + "_workforce", IdentityUserID: employeeUserID, OrganizationUnitID: "company", ManagerIdentityUserID: managerUserID}
}

func (d *integrationTestIdentityDirectory) FindUser(_ context.Context, lookup identitysdk.UserLookup) (identitysdk.User, bool, error) {
	user, found := d.users[string(lookup.UserID)]
	return user, found, nil
}

func (d *integrationTestIdentityDirectory) FindDepartment(_ context.Context, lookup identitysdk.DepartmentLookup) (identitysdk.Department, bool, error) {
	department, found := d.departments[lookup.DepartmentID]
	return department, found, nil
}

func (d *integrationTestIdentityDirectory) ListUsers(context.Context, identitysdk.DirectoryQuery) ([]identitysdk.User, error) {
	values := make([]identitysdk.User, 0, len(d.users))
	for _, user := range d.users {
		values = append(values, user)
	}
	sort.Slice(values, func(i, j int) bool { return values[i].ID < values[j].ID })
	return values, nil
}

func (d *integrationTestIdentityDirectory) ListRoles(context.Context, identitysdk.DirectoryQuery) ([]identitysdk.Role, error) {
	return nil, nil
}

func (d *integrationTestIdentityDirectory) ListUserRoleAssignments(context.Context, identitysdk.UserRoleAssignmentQuery) ([]identitysdk.UserRoleAssignment, error) {
	return nil, nil
}

func (d *integrationTestIdentityDirectory) ListWorkforce(context.Context, identitysdk.DirectoryQuery) ([]identitysdk.WorkforceEntry, error) {
	values := make([]identitysdk.WorkforceEntry, 0, len(d.workforce))
	for _, entry := range d.workforce {
		values = append(values, entry)
	}
	sort.Slice(values, func(i, j int) bool { return values[i].IdentityUserID < values[j].IdentityUserID })
	return values, nil
}

func objectActionTestDependencies(store *persistence.RuntimeStore) RuntimeServicesDependencies {
	if store == nil {
		return RuntimeServicesDependencies{}
	}
	return RuntimeServicesDependencies{
		Records:              recordpersistence.NewRecordStore(store),
		Audit:                auditpersistence.NewAuditStore(store),
		IntegrationConfig:    integrationpersistence.NewIntegrationConfigStore(store),
		IntegrationEvents:    integrationpersistence.NewIntegrationEventStore(store),
		IntegrationDelivery:  integrationpersistence.NewIntegrationDeliveryStore(store),
		IntegrationWorker:    integrationpersistence.NewIntegrationWorkerStore(store),
		Metadata:             metadatapersistence.NewMetadataStore(store),
		WorkflowWorker:       workflowpersistence.NewWorkflowWorkerStore(store),
		WorkflowDefinitions:  workflowpersistence.NewWorkflowDefinitionStore(store),
		WorkflowProcesses:    workflowpersistence.NewWorkflowProcessStore(store),
		WorkflowDecisions:    workflowpersistence.NewWorkflowDecisionStore(store),
		AutomationWorker:     automationpersistence.NewAutomationWorkerStore(store),
		AutomationExecutions: automationpersistence.NewAutomationExecutionStore(store),
		BusinessChangePlans:  changeplanpersistence.NewBusinessChangePlanStore(store),
		BusinessEvidence:     changeplanpersistence.NewBusinessEvidenceStore(store),
		ActionExecutions:     actionpersistence.NewActionBusinessExecutionStore(store),
	}
}

func recordLegacyStore(store *persistence.RuntimeStore) recordpersistence.RecordStore {
	return recordpersistence.NewRecordStore(store)
}

func integrationConfigRepository(store *persistence.RuntimeStore) integrationpersistence.IntegrationConfigStore {
	return integrationpersistence.NewIntegrationConfigStore(store)
}

func integrationDeliveryRepository(store *persistence.RuntimeStore) integrationpersistence.IntegrationDeliveryStore {
	return integrationpersistence.NewIntegrationDeliveryStore(store)
}

func serviceErrorCode(err error) string {
	return apperror.CodeOf(err)
}

func mapFromAny(value any) map[string]any {
	mapped, _ := value.(map[string]any)
	return mapped
}

func auditStore(store *persistence.RuntimeStore) auditpersistence.AuditStore {
	return auditpersistence.NewAuditStore(store)
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
	return workflowProcessStore(store).ListProcesses(t.Context(), "default", workflowmodel.WorkflowProcessFilter{Status: status, ObjectKey: objectKey, RecordID: recordID, Limit: limit})
}

func mustUpsertIdentityUser(t *testing.T, directory *integrationTestIdentityDirectory, user identitysdk.User) {
	t.Helper()
	if strings.TrimSpace(user.Status) == "" {
		user.Status = identitysdk.UserStatusActive
	}
	directory.upsertUser(user)
}

func mustUpsertWorkforceReportingLine(t *testing.T, directory *integrationTestIdentityDirectory, managerUserID, employeeUserID string) {
	t.Helper()
	directory.setManager(managerUserID, employeeUserID)
}
