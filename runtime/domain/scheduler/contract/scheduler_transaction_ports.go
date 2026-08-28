package contract

import (
	"context"

	auditmodel "github.com/domainry/domainry-runtime/runtime/domain/audit/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

// SchedulerRunCommandTransactionPorts is the minimum durable capability set
// for a scheduler caller command. It intentionally excludes reads, workflow
// execution, connectors, files, and the global Runtime store.
type SchedulerRunCommandTransactionPorts interface {
	UpdateRunIfCurrent(context.Context, definitionmodel.ObjectSchema, recordmodel.Record, map[string]any) (bool, error)
	AppendRunEvent(context.Context, string, string, string, map[string]any) error
	InsertAudit(context.Context, auditmodel.AuditEvent) error
}
