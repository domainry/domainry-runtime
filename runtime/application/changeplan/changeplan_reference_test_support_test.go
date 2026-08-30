package changeplan

import (
	identitysdk "github.com/domainry/domainry-identity-sdk"
	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	changeplanprojection "github.com/domainry/domainry-runtime/runtime/domain/changeplan/projection"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type ReferenceGraph = changeplanmodel.ReferenceGraph
type ReferenceGraphBuilder = changeplanprojection.ChangePlanReferenceGraphBuilder

func NewReferenceGraphBuilder() *ReferenceGraphBuilder {
	return changeplanprojection.NewChangePlanReferenceGraphBuilder()
}

func changePlanAdmin() principalmodel.Principal {
	return principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "admin", WorkspaceID: "default"}}
}
