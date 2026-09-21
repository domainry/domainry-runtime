package composition

import (
	"context"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	publicationmodel "github.com/domainry/domainry-runtime/runtime/domain/publication/model"
	"strings"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	"github.com/domainry/domainry-foundation/apperror"
	auditapplication "github.com/domainry/domainry-runtime/runtime/application/auditbinding"
	deployment "github.com/domainry/domainry-runtime/runtime/application/deployment"
	principalapplication "github.com/domainry/domainry-runtime/runtime/application/principal"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	recordmutation "github.com/domainry/domainry-runtime/runtime/application/recordmutation"
	actionservice "github.com/domainry/domainry-runtime/runtime/domain/action/service"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func buildRecordApplicationDependencies(s *runtimeAssembly) recordapplication.RecordApplicationDependencies {
	workflowTriggers := recordApplicationRuntimeAdapter{records: s}
	assurance := actionservice.NewActionAssuranceDomainService(s.actionAssuranceStore, nil)
	batchBusinessPrincipals := principalapplication.NewBusinessPrincipalApplicationService(principalapplication.BusinessPrincipalDependencies{
		Records: s.recordRepo,
		Objects: func() []definitionmodel.ObjectSchema {
			return append([]definitionmodel.ObjectSchema(nil), s.Schema().Objects...)
		},
		Extensions: func() []profilebindingmodel.Binding {
			s.mu.RLock()
			defer s.mu.RUnlock()
			return append([]profilebindingmodel.Binding(nil), s.identityProfileExtensions...)
		},
	})
	var revisionResolver recordmutation.MutationMetadataRevisionResolver
	if s.applicationSchemaRepo != nil {
		revisionResolver = func(ctx context.Context, _ principalmodel.Principal) (string, error) {
			return s.applicationSchemaRepo.SnapshotRevision(ctx, principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "plan canonical record mutation"))
		}
	}
	return recordapplication.RecordApplicationDependencies{
		Repository:              s.recordRepo,
		MutationKernel:          recordmutation.NewMutationKernelApplicationService(s.recordRepo, revisionResolver),
		QueryPolicy:             s.RecordQueryPolicyDomainService,
		Pipeline:                s.PipelineApplicationService,
		Validation:              s.RecordValidationDomainService,
		IdentityProjection:      s.identityProjection,
		Audit:                   s.auditApplicationService.AppendWithMetadata,
		BuildAudit:              auditapplication.AuditBuildEvent,
		RecordMutationExecution: s.RecordMutationExecutionRuntime,
		DataExchange:            s.dataExchange,
		DataExchangeProviders:   s.dataExchangeProviders,
		ValidateFileReferences:  s.validateFileReferences,
		ValidateExportAssurance: func(ctx context.Context, object definitionmodel.ObjectSchema, principal principalmodel.Principal, intent map[string]any, token string) (map[string]string, error) {
			policy := object.ExportAssurancePolicy
			if policy == nil || len(policy.RequiredMethods) == 0 {
				return nil, nil
			}
			if actionAssuranceOnlyNormalLogin(policy.RequiredMethods) {
				return map[string]string{"methods": definitionmodel.ActionAssuranceNormalLogin}, nil
			}
			action := definitionmodel.ActionSchema{Key: definitionmodel.ObjectExportAssuranceActionKey(strings.TrimSpace(object.Key)), ObjectKey: object.Key, AssurancePolicy: policy}
			evidence, err := assurance.ValidateAndConsume(ctx, action, principal.WorkspaceID, principal.UserID, object.Key, "", intent, token)
			if err != nil {
				return nil, apperror.FromError(apperror.KindForbidden, err)
			}
			return map[string]string{
				"grant_id": evidence.GrantID, "methods": strings.Join(evidence.Methods, ","), "approval_version": evidence.ApprovalVersion,
				"approval_hash": evidence.ApprovalHash, "payload_digest": evidence.Facts["payload_digest"],
			}, nil
		},
		ResolveBatchPrincipal: func(ctx context.Context, userID, roleKey string) principalmodel.Principal {
			// Batch jobs must re-authorize through the same persisted Identity
			// projection used by HTTP prepare. The manifest-only principal service
			// does not carry user-role assignments, organization facts, or the
			// authorization revision and therefore cannot reproduce a frozen
			// Report authorization-scope hash.
			if s.identityPrincipals == nil {
				return principalmodel.Principal{Principal: identitysdk.Principal{Known: false}}
			}
			resolution, err := s.identityPrincipals.Resolve(ctx, identitysdk.PrincipalResolutionRequest{SubjectID: identitysdk.SubjectID(userID), SessionRoleKey: roleKey})
			if err != nil {
				return principalmodel.Principal{Principal: identitysdk.Principal{Known: false}}
			}
			resolution.Principal.AccessBundle = &resolution.AccessBundle
			principal := principalmodel.NewPrincipalFromIdentity(resolution.Principal, "")
			// HTTP prepare always resolves the default business-principal facts,
			// even when the project defines no active profile. Re-run that owner
			// here so its authorization revision and profile facts are identical.
			principal, err = batchBusinessPrincipals.ResolveBusinessPrincipal(ctx, principal, "", "")
			if err != nil {
				return principalmodel.Principal{Principal: identitysdk.Principal{Known: false}}
			}
			return principal
		},
		FindBeforeCreateReplay: func(ctx context.Context, object definitionmodel.ObjectSchema, input map[string]any, principal principalmodel.Principal) (recordmodel.Record, bool, error) {
			if s.automationApplicationService == nil {
				return recordmodel.Record{}, false, nil
			}
			return s.automationApplicationService.FindBeforeCreateReplay(ctx, object, input, principal)
		},
		RunBefore: func(ctx context.Context, objectKey, operation, recordID string, input, before, candidate map[string]any, principal principalmodel.Principal) error {
			if s.automationApplicationService == nil {
				return nil
			}
			_, err := s.automationApplicationService.RunBefore(ctx, objectKey, operation, recordID, input, before, candidate, principal)
			return err
		},
		AfterOutbox: func(objectKey, operation string, before map[string]any, record recordmodel.Record, principal principalmodel.Principal) []publicationmodel.Message {
			if s.automationApplicationService == nil {
				return nil
			}
			return s.automationApplicationService.AfterOutbox(objectKey, operation, before, record, principal)
		},
		PrepareWorkflow: func(ctx context.Context, objectKey string, record recordmodel.Record, before map[string]any, principal principalmodel.Principal, trigger string) ([]workflowmodel.WorkflowExecution, error) {
			intents, _, err := workflowTriggers.Prepare(ctx, objectKey, record, before, principal, trigger)
			return intents, err
		},
		ExecuteWorkflow:              workflowTriggers.Execute,
		ApplyStateMachineSelfEffects: s.recordStateMachineEffects.ApplySelfEffects,
		SchemaMap:                    func() map[string]definitionmodel.ObjectSchema { return schemaObjectMap(s.Schema().Objects) },
		IdentityProfileExtensions: func() []profilebindingmodel.Binding {
			s.mu.RLock()
			defer s.mu.RUnlock()
			return append([]profilebindingmodel.Binding(nil), s.identityProfileExtensions...)
		},
	}
}

func initializeIntegrationAndBusinessSystem(ctx context.Context, s *runtimeAssembly, manifest manifestmodel.ManifestSchema, deps RuntimeServicesDependencies, queryPolicy recordQueryPolicyAdapter) {
	s.actionService = assembleActionApplication(s, s, queryPolicy, s.applicationSchemaService, deps.ProjectExtensions, func(ctx context.Context, event, objectKey, recordID string, principal principalmodel.Principal, summary string, metadata map[string]any) {
		s.auditApplicationService.AppendWithMetadata(ctx, event, objectKey, recordID, principal, summary, nil, nil, metadata)
	})
	s.runtimeStatusService = deployment.NewDeploymentRuntimeStatusApplicationServiceWithWorker(s, s.schedulerDefinitionSource, deps.RuntimeStatus, deps.Records, s.auditApplicationService, deps.WorkflowWorker, nil, s.workerDependencies)
	s.workflowProcesses = assembleWorkflowProcessEngine(s)
	integrationsService := publicationHandoffApplication(s)
	s.publicationHandoffService = integrationsService
	s.businessSystemService = assembleBusinessSystemApplication(s.schemaService, s.metadataDefinitions, s.workflowApplicationService, s.automationApplicationService, integrationsService, s.recordApplicationService, s.schedulerDefinitionSource, s.runtimeStatusService, s.businessEvidenceRepo)
}
