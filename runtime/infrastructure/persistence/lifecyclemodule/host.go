// Package lifecyclemodule binds the reusable Lifecycle module to Runtime's
// already-open database, ORM renderer, migration coordinator and transaction
// boundary. It contains no Lifecycle business or persistence implementation.
package lifecyclemodule

import (
	"context"
	"database/sql"
	"fmt"

	auditsdk "github.com/domainry/domainry-audit-sdk"
	auditcontract "github.com/domainry/domainry-audit-sdk/contract"
	sharedartifact "github.com/domainry/domainry-foundation/artifact"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	"github.com/domainry/domainry-lifecycle-sdk/modulehost"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	artifactstore "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/artifact"
)

type Host struct {
	store   *database.RuntimeStore
	audit   auditsdk.Binding
	content interface {
		lifecyclecontract.ArtifactContentStore
		lifecyclecontract.ArtifactContentWriter
	}
}

func NewHost(store *database.RuntimeStore, audit auditsdk.Binding, content interface {
	lifecyclecontract.ArtifactContentStore
	lifecyclecontract.ArtifactContentWriter
}) Host {
	return Host{store: store, audit: audit, content: content}
}

func (h Host) Database() modulehost.Database { return h.store.DB() }
func (h Host) Dialect() modulehost.Dialect   { return h.store.RuntimeRenderer() }
func (h Host) Migrations() modulehost.MigrationRegistrar {
	return migrationRegistrar{store: h.store}
}
func (h Host) Transactions() modulehost.Transactor { return transactor{db: h.store.DB()} }

func (h Host) AuditAppender() auditcontract.Appender {
	if h.audit == nil {
		return nil
	}
	return h.audit.Appender()
}

func (h Host) AuditTransactionalAppender() auditcontract.TransactionalAppender {
	if h.audit == nil {
		return nil
	}
	return h.audit.TransactionalAppender()
}

func (h Host) ArtifactStore() sharedartifact.ManagedStore {
	if h.store == nil {
		return nil
	}
	return artifactstore.NewStore(h.store)
}

func (h Host) ArtifactContentStore() lifecyclecontract.ArtifactContentStore {
	return h.content
}

func (h Host) ArtifactContentWriter() lifecyclecontract.ArtifactContentWriter {
	return h.content
}

type migrationRegistrar struct{ store *database.RuntimeStore }

func (r migrationRegistrar) ApplyOwnedMigrations(ctx context.Context, owner string, values []modulehost.SchemaMigration) error {
	return r.store.ApplyORMOwnedMigrations(ctx, owner, values)
}

type transactor struct{ db *sql.DB }

func (t transactor) WithinTransaction(ctx context.Context, operation func(context.Context, modulehost.DBTX) error) error {
	if t.db == nil || operation == nil {
		return fmt.Errorf("lifecycle transaction requires database and operation")
	}
	tx, err := t.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := operation(modulehost.WithExecutor(ctx, tx), tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

var _ modulehost.Host = Host{}
var _ modulehost.AuditStoreHost = Host{}
var _ modulehost.ArtifactStoreHost = Host{}
