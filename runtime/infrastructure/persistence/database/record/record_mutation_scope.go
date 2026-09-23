package record

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/domainry/domainry-orm/query"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	querypersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/query"
)

func recordMutationScopeAllowedTx(ctx context.Context, tx TransactionExecutor, store *database.RuntimeStore, workspaceID string, object definitionmodel.ObjectSchema, recordID string, scope *recordmodel.RecordScopeExpression) (bool, error) {
	predicate, err := querypersistence.BuildTenantPredicate(store, workspaceID, recordmodel.RecordListQuery{
		AuthorizationMode: recordmodel.RecordQueryAuthorizationPredicate, RootObjectKey: object.Key, ScopeExpression: scope,
	})
	if err != nil {
		return false, fmt.Errorf("compile record mutation scope inspection: %w", err)
	}
	statement, args, err := query.NewWorkspaceSelectBuilder(store.SQLRenderer, object.Key, workspaceID).
		Columns("id").
		Where(query.And(query.Equal("id", recordID), predicate)).
		Limit(1).
		Build()
	if err != nil {
		return false, err
	}
	var found string
	err = tx.QueryRowContext(ctx, statement, args...).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect record mutation authorization scope: %w", err)
	}
	return strings.TrimSpace(found) != "", nil
}
