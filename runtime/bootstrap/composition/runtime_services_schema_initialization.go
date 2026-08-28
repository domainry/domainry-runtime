package composition

import (
	auditapplication "github.com/domainry/domainry-runtime/runtime/application/audit"
	metadataapplication "github.com/domainry/domainry-runtime/runtime/application/metadata"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	recordservice "github.com/domainry/domainry-runtime/runtime/domain/record/service"
)

func initializeSchemaAndRecordFoundation(s *runtimeAssembly, deps RuntimeServicesDependencies) recordQueryPolicyAdapter {
	if s.auditApplicationService == nil {
		s.auditApplicationService = auditapplication.NewAuditApplicationService(deps.Audit, deps.AuditExports)
	}
	s.RecordScopeOwnerFactDerivationService = recordservice.NewRecordScopeOwnerFactDerivationDomainService(recordservice.RecordScopeOwnerFactDerivationDependencies{
		WorkforceDirectory: deps.IdentityDirectory,
	})
	s.internalMutations = recordapplication.NewRecordInternalMutationApplicationService(recordapplication.RecordInternalMutationDependencies{
		Repository: deps.Records,
		Audit:      s.auditApplicationService.AppendWithMetadata,
	})
	s.schemaService = metadataapplication.NewMetadataSchemaApplicationService(s, deps.Metadata)
	s.RecordQueryPolicyDomainService = newRecordQueryPolicyService(s)
	queryPolicy := recordQueryPolicyAdapter{service: s.RecordQueryPolicyDomainService}
	s.recordStateMachineEffects = newRecordStateMachineEffects()
	s.RecordValidationDomainService = newRecordValidationService(s, deps.Records, queryPolicy, deps.IdentityDirectory, deps.PartyDirectory)
	s.PipelineApplicationService = newPipelineApplicationService(s, deps.Records, queryPolicy)
	s.ActionPreconditionApplicationService = newActionPreconditionService(s.PipelineApplicationService)
	return queryPolicy
}
