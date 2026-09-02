package record

import (
	"context"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	pipelineapplication "github.com/domainry/domainry-runtime/runtime/application/pipeline"
	recordmutation "github.com/domainry/domainry-runtime/runtime/application/recordmutation"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordservice "github.com/domainry/domainry-runtime/runtime/domain/record/service"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type recordCompositionDirectory struct{}

func (recordCompositionDirectory) FindUser(context.Context, identitysdk.UserLookup) (identitysdk.User, bool, error) {
	return identitysdk.User{}, false, nil
}
func (recordCompositionDirectory) FindOrganizationUnit(context.Context, identitysdk.OrganizationUnitLookup) (identitysdk.OrganizationUnit, bool, error) {
	return identitysdk.OrganizationUnit{}, false, nil
}
func (recordCompositionDirectory) ListUsers(context.Context, identitysdk.DirectoryQuery) ([]identitysdk.User, error) {
	return []identitysdk.User{{ID: "user-1"}}, nil
}
func (recordCompositionDirectory) ListRoles(context.Context, identitysdk.DirectoryQuery) ([]identitysdk.Role, error) {
	return nil, nil
}
func (recordCompositionDirectory) ListUserRoleAssignments(context.Context, identitysdk.UserRoleAssignmentQuery) ([]identitysdk.UserRoleAssignment, error) {
	return nil, nil
}

func TestRecordApplicationCompositionForwardsEveryOwnedClosure(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}, {Key: "owner_id", Type: "relation"}}}
	objects := map[string]definitionmodel.ObjectSchema{object.Key: object}
	repository := &recordQueryRepositoryProbe{objects: objects}
	queryPolicy := recordservice.NewRecordQueryPolicyDomainService(recordservice.RecordQueryPolicyDependencies{Objects: func() []definitionmodel.ObjectSchema { return []definitionmodel.ObjectSchema{object} }})
	validation := recordservice.NewRecordValidationDomainService(recordservice.RecordValidationDependencies{
		Repository: repository,
		Object: func(_ context.Context, key string) (definitionmodel.ObjectSchema, bool) {
			value, ok := objects[key]
			return value, ok
		},
		CanAccess: func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return true },
	})
	pipeline := pipelineapplication.NewPipelineApplicationService(pipelineapplication.PipelineDependencies{
		Repository: repository,
		Object: func(_ context.Context, key string) (definitionmodel.ObjectSchema, bool) {
			value, ok := objects[key]
			return value, ok
		},
		CanAccess: func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return true },
	})
	service := NewRecordApplicationService(RecordApplicationDependencies{
		Repository: repository, QueryPolicy: queryPolicy, Pipeline: pipeline, Validation: validation,
		MutationKernel:            recordmutation.NewMutationKernelApplicationService(repository, nil),
		IdentityDirectory:         recordCompositionDirectory{},
		SchemaMap:                 func() map[string]definitionmodel.ObjectSchema { return objects },
		IdentityProfileExtensions: func() []profilebindingmodel.Binding { return nil },
		RunBefore: func(context.Context, string, string, string, map[string]any, map[string]any, map[string]any, principalmodel.Principal) error {
			return nil
		},
		PrepareWorkflow: func(context.Context, string, recordmodel.Record, map[string]any, principalmodel.Principal, string) ([]workflowmodel.WorkflowExecution, error) {
			return nil, nil
		},
		ExecuteWorkflow: func(context.Context, []workflowmodel.WorkflowExecution, principalmodel.Principal) {},
	})
	ctx := t.Context()
	principalBundle := recordFullAccessBundle()
	principalBundle.FieldPolicies = append(principalBundle.FieldPolicies, accessfixture.FieldPolicyFixture{
		ObjectKey: "customer", FieldKey: "name",
		Policies: []accessfixture.FieldRuleFixture{{
			Key: "owner-write", Actions: []string{"write"}, Effect: "allow",
			Predicate: &accessfixture.PredicateFixture{Operator: "eq", FieldKey: "owner_id", ValueSource: "actor_claim", ClaimKey: "user_id"},
		}},
	})
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "admin"}}, principalBundle)
	record := recordmodel.Record{ID: "customer-1", Data: map[string]any{"name": "Acme", "owner_id": "admin"}}

	_ = service.create.dependencies.ValidatePipeline(ctx, object, record.ID, record.Data, principal)
	_ = service.create.dependencies.ApplyPipelineDefaults(ctx, object, record.Data, principal, false)
	_ = service.create.dependencies.RunBefore(ctx, object.Key, "create", record.ID, nil, nil, record.Data, principal)
	_ = service.create.dependencies.ValidateRelations(ctx, object, record.Data, principal)
	_ = service.create.dependencies.ValidatePolicies(ctx, object, nil, record.Data, record.ID, "create", principal)
	_ = service.create.dependencies.ValidateFields(ctx, object, record, map[string]any{"name": "Acme"}, principal)
	_, _ = service.create.dependencies.PrepareWorkflow(ctx, object.Key, record, nil, principal, "record_created:customer")
	_ = service.restore.dependencies.ValidateRelations(ctx, object, record.Data, principal)
	_ = service.restore.dependencies.ValidatePolicies(ctx, object, record.Data, record.Data, record.ID, "restore", principal)
	_, _ = service.restore.dependencies.PrepareWorkflow(ctx, object.Key, record, record.Data, principal, "record_restored:customer")

	_ = service.update.dependencies.ValidatePipeline(ctx, object, record.ID, record.Data, principal)
	_ = service.update.dependencies.ApplyPipelineDefaults(ctx, object, record.Data, principal, false)
	_ = service.update.dependencies.RunBefore(ctx, object.Key, "update", record.ID, nil, record.Data, record.Data, principal)
	_ = service.update.dependencies.ValidateRelations(ctx, object, record.Data, principal)
	_ = service.update.dependencies.ValidatePolicies(ctx, object, record.Data, record.Data, record.ID, "update", principal)
	_ = service.update.dependencies.ValidateFields(ctx, object, record, map[string]any{"name": "Acme"}, principal)
	_, _ = service.update.dependencies.PrepareWorkflow(ctx, object.Key, record, record.Data, principal, "record_updated:customer")

	_ = service.delete.dependencies.RunBefore(ctx, object.Key, "delete", record.ID, nil, record.Data, record.Data, principal)
	_ = service.delete.dependencies.ValidatePolicies(ctx, object, record.Data, record.Data, record.ID, "delete", principal)
	_, _ = service.delete.dependencies.PrepareWorkflow(ctx, object.Key, record, record.Data, principal, "record_deleted:customer")
	_, _, _ = service.delete.dependencies.PlanUpdateReference(ctx, recordservice.RecordDeleteReference{Object: definitionmodel.ObjectSchema{Key: "missing"}, Record: record, Field: definitionmodel.FieldSchema{Key: "customer"}}, principal)

	_ = service.importer.dependencies.ValidateRelations(ctx, object, record.Data, principal)
	_, _ = service.importer.dependencies.CreateRecord(ctx, "missing", record.Data, principal)
	users, err := service.exporter.dependencies.ListDirectoryUsers(ctx)
	if err != nil || len(users) != 1 {
		t.Fatalf("directory users=%#v err=%v", users, err)
	}
}
