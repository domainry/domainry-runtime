package changeplan

import (
	changeplancontract "github.com/domainry/domainry-runtime/runtime/domain/changeplan/contract"
	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	changeplanprojection "github.com/domainry/domainry-runtime/runtime/domain/changeplan/projection"
	changeplanvalidation "github.com/domainry/domainry-runtime/runtime/domain/changeplan/validation"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

const BusinessSystemChangePlanVersion = changeplanmodel.BusinessSystemChangePlanVersion

type BusinessSystemChangePlan = changeplanmodel.BusinessSystemChangePlan
type BusinessSystemChangeItem = changeplanmodel.BusinessSystemChangeItem
type BusinessReferenceMigration = changeplanmodel.BusinessReferenceMigration
type BusinessChangeTarget = changeplanmodel.BusinessChangeTarget
type BusinessChangePlanValidationIssue = changeplanmodel.BusinessChangePlanValidationIssue
type FrontendCapabilities = changeplanmodel.FrontendCapabilities
type FrontendManifest = changeplanmodel.FrontendManifest
type FrontendRequirement = changeplanmodel.FrontendRequirement
type FrontendSupportEntry = changeplanmodel.FrontendSupportEntry
type ReferenceGraph = changeplanmodel.ReferenceGraph
type ReferenceEdge = changeplanmodel.ReferenceEdge
type ReferenceNode = changeplanmodel.ReferenceNode
type ReferenceImpact = changeplanmodel.ReferenceImpact

const BusinessReferenceGraphVersion = changeplanprojection.ChangePlanReferenceGraphVersion

func NewReferenceGraphBuilder() *changeplanprojection.ChangePlanReferenceGraphBuilder {
	return changeplanprojection.NewChangePlanReferenceGraphBuilder()
}

func ValidateBusinessSystemChangePlan(plan changeplanmodel.BusinessSystemChangePlan, snapshot changeplancontract.SnapshotSource, graph changeplancontract.ReferenceGraphSource, _ principalmodel.Principal) (changeplanmodel.BusinessChangePlanValidation, error) {
	snapshotValue, graphValue := snapshot.ChangePlanSnapshot(), graph.ChangePlanReferenceGraph()
	result := changeplanvalidation.ValidateBusinessSystemChangePlan(plan, snapshotValue, graphValue)
	for _, item := range plan.Items {
		result.Diffs = append(result.Diffs, changeplanprojection.BuildChangePlanBusinessDiff(item, snapshotValue, graphValue))
	}
	return result, nil
}

func BuildBusinessChangeDiff(item changeplanmodel.BusinessSystemChangeItem, snapshot changeplancontract.SnapshotSource, graph changeplancontract.ReferenceGraphSource) changeplanmodel.BusinessChangeDiff {
	return changeplanprojection.BuildChangePlanBusinessDiff(item, snapshot.ChangePlanSnapshot(), graph.ChangePlanReferenceGraph())
}

func changePlanWithoutPermissions(principal principalmodel.Principal) principalmodel.Principal {
	accessfixture.Set(&principal, accessfixture.Bundle{})
	return principal
}
