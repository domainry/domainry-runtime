package runtime

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	devdata "github.com/domainry/domainry-runtime/runtime/application/devdata"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	projectmodel "github.com/domainry/domainry-runtime/runtime/domain/project/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func generateDevelopmentData(ctx context.Context, cfg config.Config, store *persistence.RuntimeStore, model projectmodel.RuntimeModel, options DevelopmentDataOptions) error {
	if !options.Enabled {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Environment)) {
	case "development", "dev", "demo", "test":
	default:
		return fmt.Errorf("development data is forbidden in environment %q", cfg.Environment)
	}
	_, err := devdata.GenerateIfEmpty(ctx, developmentDataStore{runtime: store, records: recordpersistence.NewRecordStore(store)}, cfg.IdentityWorkspaceID, model, devdata.Options{
		Seed: options.Seed, RecordsPerObject: options.RecordsPerObject,
	})
	return err
}

type developmentDataStore struct {
	runtime *persistence.RuntimeStore
	records recordpersistence.RecordStore
}

func (s developmentDataStore) ListRecords(ctx context.Context, workspaceID string, object definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	return s.records.ListRecords(ctx, workspaceID, object, query)
}

func (s developmentDataStore) InsertRecord(ctx context.Context, workspaceID string, object definitionmodel.ObjectSchema, record recordmodel.Record) error {
	return s.records.InsertRecord(ctx, workspaceID, object, record)
}

func (s developmentDataStore) InTransaction(ctx context.Context, run func(context.Context) error) error {
	tx, err := s.runtime.DB().BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("begin development data transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := run(recordpersistence.WithActionExecutionTransaction(ctx, tx)); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit development data: %w", err)
	}
	committed = true
	return nil
}
