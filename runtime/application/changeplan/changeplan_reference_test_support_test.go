package changeplan

import (
	identitysdk "github.com/domainry/domainry-identity-sdk"
	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	changeplanprojection "github.com/domainry/domainry-runtime/runtime/domain/changeplan/projection"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

type ReferenceGraph = changeplanmodel.ReferenceGraph
type ReferenceGraphBuilder = changeplanprojection.ChangePlanReferenceGraphBuilder

func NewReferenceGraphBuilder() *ReferenceGraphBuilder {
	return changeplanprojection.NewChangePlanReferenceGraphBuilder()
}

func changePlanAdmin() principalmodel.Principal {
	return accessfixture.Attach(
		principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "admin", WorkspaceID: "workspace-primary"}},
		accessfixture.Bundle{Permissions: []string{ActionBusinessReferenceGraph, ActionBusinessReferenceImpact}},
	)
}
