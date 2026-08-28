package integrationnotification

import (
	"context"
	"database/sql"
	"fmt"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	integrationpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/integration"
	notificationpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/notification"
)

// IntegrationCredentialNotificationCommitter is a composition adapter: it
// coordinates two owner-specific persistence adapters without making either
// owner depend on the other.
type IntegrationCredentialNotificationCommitter struct {
	store         *database.RuntimeStore
	config        integrationpersistence.IntegrationConfigStore
	notifications notificationpersistence.InboxEventWriter
	begin         func(context.Context) (*sql.Tx, error)
	commit        func(*sql.Tx) error
}

func NewIntegrationCredentialNotificationCommitter(store *database.RuntimeStore) IntegrationCredentialNotificationCommitter {
	committer := IntegrationCredentialNotificationCommitter{
		store: store, config: integrationpersistence.NewIntegrationConfigStore(store), notifications: notificationpersistence.NewInboxEventWriter(store),
	}
	committer.begin = func(ctx context.Context) (*sql.Tx, error) {
		return store.DB().BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	}
	committer.commit = func(tx *sql.Tx) error { return tx.Commit() }
	return committer
}

func (s IntegrationCredentialNotificationCommitter) CommitIntegrationConnectionNotification(ctx context.Context, connection integrationmodel.IntegrationConnection, event notificationmodel.NotificationEvent) (integrationmodel.IntegrationConnection, error) {
	tx, err := s.begin(ctx)
	if err != nil {
		return integrationmodel.IntegrationConnection{}, fmt.Errorf("begin integration connection notification: %w", err)
	}
	defer tx.Rollback()
	txCtx := database.WithActionExecutionTransaction(ctx, tx)
	saved, err := s.config.UpsertConnection(txCtx, connection.WorkspaceID, connection)
	if err != nil {
		return integrationmodel.IntegrationConnection{}, err
	}
	event.WorkspaceID = connection.WorkspaceID
	if err := s.notifications.InsertEventTx(txCtx, tx, event); err != nil {
		return integrationmodel.IntegrationConnection{}, err
	}
	if err := s.commit(tx); err != nil {
		return integrationmodel.IntegrationConnection{}, fmt.Errorf("commit integration connection notification: %w", err)
	}
	s.notifications.PublishCommittedEventWakeup(event)
	return saved, nil
}

func (s IntegrationCredentialNotificationCommitter) CommitIntegrationSecretNotification(ctx context.Context, secret integrationmodel.IntegrationSecret, rawMaterial string, event notificationmodel.NotificationEvent) (integrationmodel.IntegrationSecret, error) {
	tx, err := s.begin(ctx)
	if err != nil {
		return integrationmodel.IntegrationSecret{}, fmt.Errorf("begin integration secret notification: %w", err)
	}
	defer tx.Rollback()
	txCtx := database.WithActionExecutionTransaction(ctx, tx)
	if rawMaterial != "" {
		if err := s.config.PutSecretMaterial(txCtx, secret.WorkspaceID, secret.Key, rawMaterial); err != nil {
			return integrationmodel.IntegrationSecret{}, err
		}
	}
	saved, err := s.config.UpsertSecret(txCtx, secret.WorkspaceID, secret)
	if err != nil {
		return integrationmodel.IntegrationSecret{}, err
	}
	event.WorkspaceID = secret.WorkspaceID
	if err := s.notifications.InsertEventTx(txCtx, tx, event); err != nil {
		return integrationmodel.IntegrationSecret{}, err
	}
	if err := s.commit(tx); err != nil {
		return integrationmodel.IntegrationSecret{}, fmt.Errorf("commit integration secret notification: %w", err)
	}
	s.notifications.PublishCommittedEventWakeup(event)
	return saved, nil
}
