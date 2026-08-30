package composition

import (
	appschemaapplication "github.com/domainry/domainry-runtime/runtime/application/appschema"
	changeplanapplication "github.com/domainry/domainry-runtime/runtime/application/changeplan"
	appschemarepository "github.com/domainry/domainry-runtime/runtime/domain/appschema/repository"
	auditrepository "github.com/domainry/domainry-runtime/runtime/domain/audit/repository"
	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	changeplanprojection "github.com/domainry/domainry-runtime/runtime/domain/changeplan/projection"
	changeplanrepository "github.com/domainry/domainry-runtime/runtime/domain/changeplan/repository"
)

type BusinessChangePlanRepository = changeplanrepository.ChangePlanRepository
type BusinessEvidenceRepository = changeplanrepository.ChangePlanEvidenceRepository
type businessChangePlanMetadataRuntimeAdapter = changePlanMetadataRuntimeAdapter
type BusinessReferenceGraph = changeplanmodel.ReferenceGraph
type BusinessReferenceNode = changeplanmodel.ReferenceNode
type BusinessReferenceEdge = changeplanmodel.ReferenceEdge
type BusinessReferenceImpact = changeplanmodel.ReferenceImpact

const BusinessReferenceGraphVersion = changeplanprojection.ChangePlanReferenceGraphVersion

func NewBusinessChangePlanApplicationService(repository changeplanrepository.ChangePlanRepository, metadata appschemarepository.ApplicationSchemaRepository, audit auditrepository.AuditRepository, runtime *appschemaapplication.ApplicationSchemaApplicationService) *changeplanapplication.ChangePlanApplicationService {
	return newChangePlanApplicationService(repository, metadata, audit, runtime)
}
