package changeplan

import changeplanprojection "github.com/domainry/domainry-runtime/runtime/domain/changeplan/projection"

import (
	"fmt"
)

func AddPresentationReferences(builder *changeplanprojection.ChangePlanReferenceGraphBuilder, snapshot ReferenceSchema) {
	// Source-owned frontend references come from registered frontend capability
	// evidence. Frontend layout payloads are intentionally ignored:
	// they describe an obsolete schema-driven renderer rather than deployed
	// source code.
	for _, agent := range snapshot.Agents {
		builder.Node("agent", agent.Key, "", agent.Name, "")
		for index, reportKey := range referenceStringList(agent.Config["report_keys"]) {
			builder.Edge("agent", agent.Key, "report", reportKey, "reads_report", fmt.Sprintf("config.report_keys[%d]", index))
		}
	}
}
