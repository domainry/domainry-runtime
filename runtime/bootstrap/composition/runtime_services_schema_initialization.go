package composition

import (
	appschemaapplication "github.com/domainry/domainry-runtime/runtime/application/appschema"
	auditapplication "github.com/domainry/domainry-runtime/runtime/application/auditbinding"
)

func initializeSchemaAndRecordFoundation(s *runtimeAssembly, deps RuntimeServicesDependencies) recordQueryPolicyAdapter {
	if s.auditApplicationService == nil {
		s.auditApplicationService = auditapplication.NewAuditApplicationService(deps.Audit)
	}
	s.schemaService = appschemaapplication.NewApplicationSchemaQueryApplicationService(s, deps.MetadataLocalization)
	s.RecordQueryPolicyDomainService = newRecordQueryPolicyService(s)
	queryPolicy := recordQueryPolicyAdapter{service: s.RecordQueryPolicyDomainService}
	s.recordStateMachineEffects = newRecordStateMachineEffects()
	s.RecordValidationDomainService = newRecordValidationService(s, deps.Records, queryPolicy, deps.IdentityProjection)
	s.PipelineApplicationService = newPipelineApplicationService(s, deps.Records, queryPolicy)
	s.ActionPreconditionApplicationService = newActionPreconditionService(s.PipelineApplicationService)
	return queryPolicy
}
