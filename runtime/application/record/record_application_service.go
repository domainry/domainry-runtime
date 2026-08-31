package record

import (
	dataexchange "github.com/domainry/domainry-data-exchange-sdk"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	publicationmodel "github.com/domainry/domainry-runtime/runtime/domain/publication/model"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	pipelineapplication "github.com/domainry/domainry-runtime/runtime/application/pipeline"
	recordmutation "github.com/domainry/domainry-runtime/runtime/application/recordmutation"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	recordruntime "github.com/domainry/domainry-runtime/runtime/domain/record/runtime"
	recordservice "github.com/domainry/domainry-runtime/runtime/domain/record/service"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"

	"context"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"github.com/domainry/domainry-foundation/apperror"
	workflowpolicy "github.com/domainry/domainry-runtime/runtime/domain/workflow/policy"
)

type RecordApplicationService struct {
	*recordservice.RecordDomainService
	create                    *RecordCreateApplicationService
	update                    *RecordUpdateApplicationService
	delete                    *RecordDeleteApplicationService
	restore                   *RecordRestoreApplicationService
	importer                  *RecordImportApplicationService
	exporter                  *RecordExportApplicationService
	dataExchange              *RecordDataExchangeApplicationService
	ownerDepartmentPaths      *RecordOwnerDepartmentPathApplicationService
	queryPolicy               *recordservice.RecordQueryPolicyDomainService
	audit                     func(context.Context, string, string, string, principalmodel.Principal, string, map[string]any, map[string]any, map[string]any)
	prepareWorkflow           func(context.Context, string, recordmodel.Record, map[string]any, principalmodel.Principal, string) ([]workflowmodel.WorkflowExecution, error)
	executeWorkflow           func(context.Context, []workflowmodel.WorkflowExecution, principalmodel.Principal)
	updateInternal            func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, string) error
	schemaMap                 func() map[string]definitionmodel.ObjectSchema
	identityProfileExtensions func() []profilebindingmodel.Binding
	recordMutationExecution   *recordruntime.RecordMutationExecutionRuntime
	contextualFieldPolicy     *recordservice.RecordContextualFieldPolicyDomainService
}

type RecordApplicationDependencies struct {
	Repository                   recordrepository.RecordRepository
	MutationKernel               *recordmutation.MutationKernelApplicationService
	QueryPolicy                  *recordservice.RecordQueryPolicyDomainService
	Pipeline                     *pipelineapplication.PipelineApplicationService
	Validation                   *recordservice.RecordValidationDomainService
	IdentityDirectory            identitysdk.Directory
	ScopeOwnerFactDerivation     *recordservice.RecordScopeOwnerFactDerivationDomainService
	Audit                        func(context.Context, string, string, string, principalmodel.Principal, string, map[string]any, map[string]any, map[string]any)
	PrepareWorkflow              func(context.Context, string, recordmodel.Record, map[string]any, principalmodel.Principal, string) ([]workflowmodel.WorkflowExecution, error)
	ExecuteWorkflow              func(context.Context, []workflowmodel.WorkflowExecution, principalmodel.Principal)
	ApplyStateMachineSelfEffects func(context.Context, definitionmodel.ObjectSchema, map[string]any, map[string]any, string, principalmodel.Principal) (bool, error)
	UpdateInternal               func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, string) error
	SchemaMap                    func() map[string]definitionmodel.ObjectSchema
	IdentityProfileExtensions    func() []profilebindingmodel.Binding
	FindBeforeCreateReplay       func(context.Context, definitionmodel.ObjectSchema, map[string]any, principalmodel.Principal) (recordmodel.Record, bool, error)
	RunBefore                    func(context.Context, string, string, string, map[string]any, map[string]any, map[string]any, principalmodel.Principal) error
	AfterOutbox                  func(string, string, map[string]any, recordmodel.Record, principalmodel.Principal) []publicationmodel.Message
	BuildAudit                   func(context.Context, string, string, string, principalmodel.Principal, string, map[string]any, map[string]any, map[string]any) auditmodel.AuditEvent
	RecordMutationExecution      *recordruntime.RecordMutationExecutionRuntime
	DataExchange                 dataexchange.Binding
	DataExchangeProviders        *DataExchangeProviders
	ResolveBatchPrincipal        func(context.Context, string, string) principalmodel.Principal
	ValidateExportAssurance      func(context.Context, definitionmodel.ObjectSchema, principalmodel.Principal, map[string]any, string) (map[string]string, error)
}

func (s *RecordApplicationService) objectForAction(principal principalmodel.Principal, objectKey, action string) (definitionmodel.ObjectSchema, error) {
	return s.queryPolicy.ObjectForAction(principal, objectKey, action)
}

func (s *RecordApplicationService) normalizeListQuery(object definitionmodel.ObjectSchema, query recordmodel.RecordListQuery, principal principalmodel.Principal) recordmodel.RecordListQuery {
	return s.queryPolicy.NormalizeListQuery(object, query, principal)
}

func (s *RecordApplicationService) canAccessRecord(principal principalmodel.Principal, object definitionmodel.ObjectSchema, record recordmodel.Record) bool {
	return s.queryPolicy.CanAccessRecord(principal, object, record)
}

func (s *RecordApplicationService) ObjectForAction(principal principalmodel.Principal, objectKey, action string) (definitionmodel.ObjectSchema, error) {
	if err := recordAuthorizeQuery(principal); err != nil {
		return definitionmodel.ObjectSchema{}, err
	}
	return s.objectForAction(principal, objectKey, action)
}

func recordAuthorizeQuery(principal principalmodel.Principal) error {
	if !principal.Known {
		return &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.workspace_scope_required"}
	}
	if _, err := principalmodel.NewWorkspaceQueryScope(principal.WorkspaceID); err != nil {
		return &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.workspace_scope_required", Err: err}
	}
	return nil
}

func recordAuthorizeCommand(principal principalmodel.Principal) error {
	if !principal.Known {
		return &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.workspace_scope_required"}
	}
	if _, err := principalmodel.NewWorkspaceCommandScope(principal.WorkspaceID); err != nil {
		return &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.workspace_scope_required", Err: err}
	}
	return nil
}

func NewRecordApplicationService(dependencies RecordApplicationDependencies) *RecordApplicationService {
	mutationKernel := dependencies.MutationKernel
	if mutationKernel == nil {
		mutationKernel = recordmutation.NewMutationKernelApplicationService(dependencies.Repository, nil)
	}
	service := &RecordApplicationService{
		queryPolicy:               dependencies.QueryPolicy,
		audit:                     dependencies.Audit,
		prepareWorkflow:           dependencies.PrepareWorkflow,
		executeWorkflow:           dependencies.ExecuteWorkflow,
		updateInternal:            dependencies.UpdateInternal,
		schemaMap:                 dependencies.SchemaMap,
		identityProfileExtensions: dependencies.IdentityProfileExtensions,
		recordMutationExecution:   dependencies.RecordMutationExecution,
	}
	contextualFieldPolicy := recordservice.NewRecordContextualFieldPolicyDomainService(recordservice.RecordContextualFieldPolicyDependencies{
		Repository: dependencies.Repository,
		Objects: func() []definitionmodel.ObjectSchema {
			objects := service.schemaMap()
			result := make([]definitionmodel.ObjectSchema, 0, len(objects))
			for _, object := range objects {
				result = append(result, object)
			}
			return result
		},
	})
	service.contextualFieldPolicy = contextualFieldPolicy
	reader := recordservice.NewRecordReadDomainService(recordservice.RecordReadDependencies{
		Repository:                dependencies.Repository,
		Policy:                    dependencies.QueryPolicy,
		IdentityProfileExtensions: service.identityProfileExtensions,
		ContextualFieldPolicy:     contextualFieldPolicy,
		AuditFieldDenials:         service.auditFieldDenials,
		AuditScopeDenial:          service.auditScopeDenial,
	})
	references := recordservice.NewRecordReferenceDomainService(recordservice.RecordReferenceDependencies{
		Repository:      dependencies.Repository,
		Objects:         service.schemaMap,
		ObjectForAction: service.queryPolicy.ObjectForAction,
		CanAccess:       service.queryPolicy.CanAccessRecord,
		ListRecords:     reader.ListRecords,
	})
	relationDeletes := recordservice.NewRecordDeleteRelationDomainService(dependencies.Repository, service.schemaMap)
	ownerDepartmentPaths := NewRecordOwnerDepartmentPathApplicationService(dependencies.Repository, service.schemaMap, service.updateInternal)
	create := NewRecordCreateApplicationService(RecordCreateDependencies{
		Repository:      dependencies.Repository,
		MutationKernel:  mutationKernel,
		ObjectForAction: service.queryPolicy.ObjectForAction,
		CanWrite:        service.queryPolicy.CanWriteRecordScope,
		CanWriteCandidate: func(ctx context.Context, principal principalmodel.Principal, object definitionmodel.ObjectSchema, candidate recordmodel.Record) (bool, error) {
			return service.queryPolicy.CanAccessRecordAction(ctx, principal, object, candidate, "create")
		},
		ApplyScopeOwnerFacts: func(ctx context.Context, workspaceID string, object definitionmodel.ObjectSchema, data map[string]any, recordID string) error {
			return dependencies.ScopeOwnerFactDerivation.Apply(ctx, workspaceID, object, data, recordID)
		},
		ValidatePipeline: func(ctx context.Context, object definitionmodel.ObjectSchema, recordID string, data map[string]any, principal principalmodel.Principal) error {
			return dependencies.Pipeline.ValidateDefaults(ctx, object, recordID, data, principal)
		},
		ApplyPipelineDefaults: func(ctx context.Context, object definitionmodel.ObjectSchema, data map[string]any, principal principalmodel.Principal, stagePatched bool) error {
			return dependencies.Pipeline.ApplyItemDefaults(ctx, object, data, principal, stagePatched)
		},
		FindReplay: dependencies.FindBeforeCreateReplay,
		RunBefore: func(ctx context.Context, objectKey, operation, recordID string, input, before, candidate map[string]any, principal principalmodel.Principal) error {
			return dependencies.RunBefore(ctx, objectKey, operation, recordID, input, before, candidate, principal)
		},
		ValidateRelations: func(ctx context.Context, object definitionmodel.ObjectSchema, data map[string]any, principal principalmodel.Principal) error {
			return dependencies.Validation.ValidateRelations(ctx, object, data, principal)
		},
		ValidatePolicies: func(ctx context.Context, object definitionmodel.ObjectSchema, before, next map[string]any, recordID, operation string, principal principalmodel.Principal) error {
			return dependencies.Validation.ValidateDomainPolicies(ctx, object, before, next, recordID, operation, principal)
		},
		ValidateFields: func(ctx context.Context, object definitionmodel.ObjectSchema, record recordmodel.Record, data map[string]any, principal principalmodel.Principal) error {
			return contextualFieldPolicy.ValidateWrite(ctx, principal, object, record, data)
		},
		ValidateUnique:    dependencies.Validation.ValidateUnique,
		ValidateDuplicate: dependencies.Validation.ValidateDuplicateIdentity,
		AfterOutbox:       dependencies.AfterOutbox,
		PrepareWorkflow: func(ctx context.Context, objectKey string, record recordmodel.Record, before map[string]any, principal principalmodel.Principal, trigger string) ([]workflowmodel.WorkflowExecution, error) {
			return service.prepareWorkflow(ctx, objectKey, record, before, principal, trigger)
		},
		ExecuteWorkflow:  service.executeWorkflow,
		Audit:            service.audit,
		BuildAudit:       dependencies.BuildAudit,
		ExecutionRuntime: dependencies.RecordMutationExecution,
	})
	restore := NewRecordRestoreApplicationService(RecordRestoreDependencies{
		Repository:      dependencies.Repository,
		MutationKernel:  mutationKernel,
		ObjectForAction: service.queryPolicy.ObjectForAction,
		CanAccess:       service.queryPolicy.CanAccessRecord,
		CanWrite:        service.queryPolicy.CanWriteRecordScope,
		ValidateRelations: func(ctx context.Context, object definitionmodel.ObjectSchema, data map[string]any, principal principalmodel.Principal) error {
			return dependencies.Validation.ValidateRelations(ctx, object, data, principal)
		},
		ValidatePolicies: func(ctx context.Context, object definitionmodel.ObjectSchema, before, next map[string]any, recordID, operation string, principal principalmodel.Principal) error {
			return dependencies.Validation.ValidateDomainPolicies(ctx, object, before, next, recordID, operation, principal)
		},
		ValidateUnique:    dependencies.Validation.ValidateUnique,
		ValidateDuplicate: dependencies.Validation.ValidateDuplicateIdentity,
		UpdatedTriggers:   workflowpolicy.WorkflowRecordUpdatedTriggers,
		PrepareWorkflow: func(ctx context.Context, objectKey string, record recordmodel.Record, before map[string]any, principal principalmodel.Principal, trigger string) ([]workflowmodel.WorkflowExecution, error) {
			return service.prepareWorkflow(ctx, objectKey, record, before, principal, trigger)
		},
		ExecuteWorkflow: service.executeWorkflow,
		BuildAudit:      dependencies.BuildAudit,
	})
	update := NewRecordUpdateApplicationService(RecordUpdateDependencies{
		Repository:      dependencies.Repository,
		MutationKernel:  mutationKernel,
		ObjectForAction: service.queryPolicy.ObjectForAction,
		CanAccess:       service.queryPolicy.CanAccessRecord,
		CanAccessScope: func(ctx context.Context, principal principalmodel.Principal, object definitionmodel.ObjectSchema, record recordmodel.Record, _ bool) (bool, error) {
			return service.queryPolicy.CanAccessRecordAction(ctx, principal, object, record, "update")
		},
		CanWrite: service.queryPolicy.CanWriteRecordScope,
		Denied: func(ctx context.Context, objectKey, recordID string, principal principalmodel.Principal, err error, reason string, patch map[string]any) {
			recordAppendUpdateDeniedAudit(ctx, service.audit, objectKey, recordID, principal, err, reason, patch)
		},
		ApplyScopeOwnerFacts: func(ctx context.Context, workspaceID string, object definitionmodel.ObjectSchema, data map[string]any, recordID string) error {
			return dependencies.ScopeOwnerFactDerivation.Apply(ctx, workspaceID, object, data, recordID)
		},
		ValidatePipeline: func(ctx context.Context, object definitionmodel.ObjectSchema, recordID string, data map[string]any, principal principalmodel.Principal) error {
			return dependencies.Pipeline.ValidateDefaults(ctx, object, recordID, data, principal)
		},
		ApplyPipelineDefaults: func(ctx context.Context, object definitionmodel.ObjectSchema, data map[string]any, principal principalmodel.Principal, stagePatched bool) error {
			return dependencies.Pipeline.ApplyItemDefaults(ctx, object, data, principal, stagePatched)
		},
		RunBefore: func(ctx context.Context, objectKey, operation, recordID string, input, before, candidate map[string]any, principal principalmodel.Principal) error {
			return dependencies.RunBefore(ctx, objectKey, operation, recordID, input, before, candidate, principal)
		},
		ValidateRelations: func(ctx context.Context, object definitionmodel.ObjectSchema, data map[string]any, principal principalmodel.Principal) error {
			return dependencies.Validation.ValidateRelations(ctx, object, data, principal)
		},
		ValidatePolicies: func(ctx context.Context, object definitionmodel.ObjectSchema, before, next map[string]any, recordID, operation string, principal principalmodel.Principal) error {
			return dependencies.Validation.ValidateDomainPolicies(ctx, object, before, next, recordID, operation, principal)
		},
		ValidateFields: func(ctx context.Context, object definitionmodel.ObjectSchema, record recordmodel.Record, data map[string]any, principal principalmodel.Principal) error {
			return contextualFieldPolicy.ValidateWrite(ctx, principal, object, record, data)
		},
		ApplySelfEffects:  dependencies.ApplyStateMachineSelfEffects,
		ValidateUnique:    dependencies.Validation.ValidateUnique,
		ValidateDuplicate: dependencies.Validation.ValidateDuplicateIdentity,
		AfterOutbox:       dependencies.AfterOutbox,
		UpdatedTriggers:   workflowpolicy.WorkflowRecordUpdatedTriggers,
		PrepareWorkflow: func(ctx context.Context, objectKey string, record recordmodel.Record, before map[string]any, principal principalmodel.Principal, trigger string) ([]workflowmodel.WorkflowExecution, error) {
			return service.prepareWorkflow(ctx, objectKey, record, before, principal, trigger)
		},
		ExecuteWorkflow:  service.executeWorkflow,
		BuildAudit:       dependencies.BuildAudit,
		ExecutionRuntime: dependencies.RecordMutationExecution,
	})
	deleteService := NewRecordDeleteApplicationService(RecordDeleteDependencies{
		Repository:      dependencies.Repository,
		MutationKernel:  mutationKernel,
		Relations:       relationDeletes,
		ObjectForAction: service.queryPolicy.ObjectForAction,
		CanAccess:       service.queryPolicy.CanAccessRecord,
		CanAccessScope: func(ctx context.Context, principal principalmodel.Principal, object definitionmodel.ObjectSchema, record recordmodel.Record, _ bool) (bool, error) {
			return service.queryPolicy.CanAccessRecordAction(ctx, principal, object, record, "delete")
		},
		CanWrite: service.queryPolicy.CanWriteRecordScope,
		RunBefore: func(ctx context.Context, objectKey, operation, recordID string, input, before, candidate map[string]any, principal principalmodel.Principal) error {
			return dependencies.RunBefore(ctx, objectKey, operation, recordID, input, before, candidate, principal)
		},
		ValidatePolicies: func(ctx context.Context, object definitionmodel.ObjectSchema, before, next map[string]any, recordID, operation string, principal principalmodel.Principal) error {
			return dependencies.Validation.ValidateDomainPolicies(ctx, object, before, next, recordID, operation, principal)
		},
		AfterOutbox:     dependencies.AfterOutbox,
		UpdatedTriggers: workflowpolicy.WorkflowRecordUpdatedTriggers,
		PrepareWorkflow: func(ctx context.Context, objectKey string, record recordmodel.Record, before map[string]any, principal principalmodel.Principal, trigger string) ([]workflowmodel.WorkflowExecution, error) {
			return service.prepareWorkflow(ctx, objectKey, record, before, principal, trigger)
		},
		ExecuteWorkflow: service.executeWorkflow,
		PlanUpdateReference: func(ctx context.Context, reference recordservice.RecordDeleteReference, principal principalmodel.Principal) (transactionmodel.MutationPlan, recordmodel.Record, error) {
			return update.PlanUpdateMutation(ctx, reference.Object.Key, reference.Record.ID, map[string]any{reference.Field.Key: nil}, principal)
		},
		BuildAudit: dependencies.BuildAudit,
	})
	importer := NewRecordImportApplicationService(RecordImportDependencies{
		Repository:      dependencies.Repository,
		ObjectForAction: service.queryPolicy.ObjectForAction,
		CanWrite:        service.queryPolicy.CanWriteRecordScope,
		ValidateRelations: func(ctx context.Context, object definitionmodel.ObjectSchema, data map[string]any, principal principalmodel.Principal) error {
			return dependencies.Validation.ValidateRelations(ctx, object, data, principal)
		},
		CreateRecord: func(ctx context.Context, objectKey string, data map[string]any, principal principalmodel.Principal) (recordmodel.Record, error) {
			return service.CreateRecord(ctx, objectKey, data, principal)
		},
		CreateIdempotent: service.CreateRecordIdempotentResult,
		Execution:        service.recordMutationExecution,
		Audit:            service.audit,
	})
	exporter := NewRecordExportApplicationService(RecordExportDependencies{
		Repository:           dependencies.Repository,
		Objects:              service.schemaMap,
		EnsureSnapshotAccess: service.queryPolicy.EnsureReportSnapshotAccess,
		NormalizeQuery:       service.queryPolicy.NormalizeListQuery,
		CanAccess:            service.queryPolicy.CanAccessRecord,
		ListRecords:          reader.ListRecords,
		ListDirectoryUsers: func(ctx context.Context) ([]identitysdk.User, error) {
			if service.IdentityDirectory() == nil {
				return nil, nil
			}
			return service.IdentityDirectory().ListUsers(ctx, identitysdk.DirectoryQuery{})
		},
		RecordDisplay: recordApplicationDisplay,
		ProjectRecords: func(ctx context.Context, principal principalmodel.Principal, object definitionmodel.ObjectSchema, records []recordmodel.Record, action string) ([]recordmodel.Record, error) {
			return service.ProjectRecordFields(ctx, principal, object, records, action)
		},
		ValidateAssurance: dependencies.ValidateExportAssurance,
		Audit:             service.audit,
	})
	if dependencies.DataExchangeProviders != nil {
		dependencies.DataExchangeProviders.ConfigureResolver(dependencies.ResolveBatchPrincipal)
		dependencies.DataExchangeProviders.Bind(importer, exporter)
	}
	dataExchange := NewRecordDataExchangeApplicationService(RecordDataExchangeDependencies{Importer: importer, Exporter: exporter, DataExchange: dependencies.DataExchange})
	service.RecordDomainService = recordservice.NewRecordDomainService(recordservice.RecordDomainServiceDependencies{
		Repository: dependencies.Repository, Reader: reader, References: references,
		IdentityDirectory: dependencies.IdentityDirectory,
	})
	service.create = create
	service.update = update
	service.delete = deleteService
	service.restore = restore
	service.importer = importer
	service.exporter = exporter
	service.dataExchange = dataExchange
	service.ownerDepartmentPaths = ownerDepartmentPaths
	return service
}

func (s *RecordApplicationService) auditFieldDenials(ctx context.Context, object definitionmodel.ObjectSchema, record recordmodel.Record, action string, denials []recordservice.RecordFieldPolicyDecision, principal principalmodel.Principal) {
	if s.audit == nil {
		return
	}
	fields := make([]string, 0, len(denials))
	rules := make([]string, 0, len(denials))
	for _, denial := range denials {
		fields = append(fields, denial.FieldKey)
		rules = append(rules, denial.RuleKey)
	}
	s.audit(ctx, "field_access_denied", object.Key, record.ID, principal, "Contextual field access denied", nil, nil, map[string]any{"action": action, "fields": fields, "policy_rules": rules})
}

func (s *RecordApplicationService) auditScopeDenial(ctx context.Context, object definitionmodel.ObjectSchema, recordID string, principal principalmodel.Principal) {
	if s.audit == nil {
		return
	}
	s.audit(ctx, "record_scope_access_denied", object.Key, recordID, principal, "Record scope access denied", nil, nil, map[string]any{"action": "read_detail", "decision": "denied"})
}
