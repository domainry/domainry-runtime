// Package contract defines storage-neutral transaction boundaries shared by
// Runtime owners. Concrete owner port sets stay in their owner package.
package contract

import "context"

// PortSet is the storage-neutral generic constraint for a deliberately named,
// owner-scoped transaction capability. Concrete owners still expose a narrow
// named interface; the generic contract does not force a marker method into
// every cross-boundary port.
type PortSet interface{}

// Operation is application-owned transactional orchestration over the
// minimum owner-specific port set supplied by a UnitOfWork implementation.
// It deliberately receives no database handle or global Runtime store.
type Operation[Ports PortSet] func(context.Context, Ports) error

// UnitOfWork executes one owner operation inside a storage transaction.
//
// Ports must be a narrow owner-defined interface or struct. Callers must not
// use RuntimeStore (or an equivalent service locator) as Ports.
type UnitOfWork[Ports PortSet] interface {
	WithinTransaction(context.Context, Operation[Ports]) error
}
