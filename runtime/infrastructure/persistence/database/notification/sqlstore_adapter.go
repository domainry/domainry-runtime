package notification

import (
	"context"
	"fmt"

	sourcenotification "github.com/domainry/domainry-notification"
	notificationsql "github.com/domainry/domainry-notification/sqlstore"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/requestcontext"
)

// NewSQLStoreAdapter composes domainry-notification persistence with Plane's
// existing database, tenant context, and shared worker-scope registry. Plane
// retains those host concerns; notification table behavior stays module-owned.
func NewSQLStoreAdapter(store *database.RuntimeStore, clock sourcenotification.Clock) (*notificationsql.Store, error) {
	if store == nil {
		return nil, fmt.Errorf("notification module store requires Runtime database")
	}
	return notificationsql.New(notificationsql.Config{
		Database: store.DB(), Dialect: runtimeNotificationDialect{store: store},
		WorkspaceScope: runtimeNotificationWorkspaceScope{},
		QueueScopes:    runtimeNotificationQueueScopes{store: store},
		Clock:          clock,
	})
}

type runtimeNotificationDialect struct{ store *database.RuntimeStore }

func (d runtimeNotificationDialect) Identifier(value string) string {
	return d.store.Identifier(value)
}

func (d runtimeNotificationDialect) Table(value string) string {
	return d.store.TableIdentifier(value)
}

func (d runtimeNotificationDialect) Placeholder(position int) string {
	return d.store.Placeholder(position)
}

func (d runtimeNotificationDialect) Insert(table string, columns []string) string {
	return d.store.InsertStatement(table, columns)
}

type runtimeNotificationWorkspaceScope struct{}

func (runtimeNotificationWorkspaceScope) Context(ctx context.Context, workspaceID sourcenotification.WorkspaceID) context.Context {
	return requestcontext.WithWorkspaceID(ctx, workspaceID.String())
}

type runtimeNotificationQueueScopes struct{ store *database.RuntimeStore }

func (q runtimeNotificationQueueScopes) Register(ctx context.Context, executor notificationsql.Executor, kind sourcenotification.WorkKind, workspaceID sourcenotification.WorkspaceID, updatedAt string) error {
	return q.store.RegisterWorkerQueueScope(ctx, executor, string(kind), workspaceID.String(), updatedAt)
}

func (q runtimeNotificationQueueScopes) Workspaces(ctx context.Context, queryer notificationsql.Queryer, kind sourcenotification.WorkKind, limit int) ([]sourcenotification.WorkspaceID, error) {
	values, err := q.store.WorkerQueueScopePage(ctx, queryer, string(kind), limit)
	if err != nil {
		return nil, err
	}
	workspaces := make([]sourcenotification.WorkspaceID, len(values))
	for index, value := range values {
		workspaces[index] = sourcenotification.WorkspaceID(value)
	}
	return workspaces, nil
}

var (
	_ notificationsql.Dialect         = runtimeNotificationDialect{}
	_ notificationsql.WorkspaceScope  = runtimeNotificationWorkspaceScope{}
	_ notificationsql.QueueScopeIndex = runtimeNotificationQueueScopes{}
)
