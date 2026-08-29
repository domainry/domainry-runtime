package composition

import (
	changeplanapplication "github.com/domainry/domainry-runtime/runtime/application/changeplan"
	metadataapplication "github.com/domainry/domainry-runtime/runtime/application/metadata"
	auditrepository "github.com/domainry/domainry-runtime/runtime/domain/audit/repository"
	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	changeplanprojection "github.com/domainry/domainry-runtime/runtime/domain/changeplan/projection"
	changeplanrepository "github.com/domainry/domainry-runtime/runtime/domain/changeplan/repository"
	metadatarepository "github.com/domainry/domainry-runtime/runtime/domain/metadata/repository"
)

type BusinessChangePlanRepository = changeplanrepository.ChangePlanRepository
type BusinessEvidenceRepository = changeplanrepository.ChangePlanEvidenceRepository
type businessChangePlanMetadataRuntimeAdapter = changePlanMetadataRuntimeAdapter
type BusinessReferenceGraph = changeplanmodel.ReferenceGraph
type BusinessReferenceNode = changeplanmodel.ReferenceNode
type BusinessReferenceEdge = changeplanmodel.ReferenceEdge
type BusinessReferenceImpact = changeplanmodel.ReferenceImpact

const BusinessReferenceGraphVersion = changeplanprojection.ChangePlanReferenceGraphVersion

func NewBusinessChangePlanApplicationService(repository changeplanrepository.ChangePlanRepository, metadata metadatarepository.MetadataRepository, audit auditrepository.AuditRepository, runtime *metadataapplication.ApplicationSchemaService) *changeplanapplication.ChangePlanApplicationService {
	return newChangePlanApplicationService(repository, metadata, audit, runtime)
}
