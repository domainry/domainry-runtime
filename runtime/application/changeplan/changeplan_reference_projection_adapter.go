package changeplan

import (
	changeplanprojection "github.com/domainry/domainry-runtime/runtime/domain/changeplan/projection"
)

func newChangePlanReferenceGraphBuilder() *changeplanprojection.ChangePlanReferenceGraphBuilder {
	return changeplanprojection.NewChangePlanReferenceGraphBuilder()
}
