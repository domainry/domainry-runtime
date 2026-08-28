package contract

import changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"

type SnapshotSource interface {
	ChangePlanSnapshot() changeplanmodel.Snapshot
}

type ReferenceGraphSource interface {
	ChangePlanReferenceGraph() changeplanmodel.ReferenceGraph
}
