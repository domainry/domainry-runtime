package workspaceprovision

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/domainry/domainry-orm/query"
	actionapplication "github.com/domainry/domainry-runtime/runtime/application/action"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

type CommercialConfigurationStore struct{ runtime *database.RuntimeStore }

func NewCommercialConfigurationStore(store *database.RuntimeStore) *CommercialConfigurationStore {
	return &CommercialConfigurationStore{runtime: store}
}

func (store *CommercialConfigurationStore) LockWorkspaceCommercialConfiguration(ctx context.Context, workspaceID string) (actionapplication.WorkspaceCommercialConfiguration, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if store == nil || store.runtime == nil || workspaceID == "" {
		return actionapplication.WorkspaceCommercialConfiguration{}, fmt.Errorf("Workspace commercial configuration store is unavailable")
	}
	executor := database.ActionExecutionTransaction(ctx)
	if executor == nil {
		return actionapplication.WorkspaceCommercialConfiguration{}, fmt.Errorf("Action transaction is required to lock Workspace commercial configuration")
	}
	builder := query.NewSelectBuilder(store.runtime.RuntimeRenderer(), "_workspace_commercial_configuration").
		Columns("max_stores", "revision").Where(query.Equal("workspace_id", workspaceID))
	builder, err := store.runtime.RuntimeProfile().ApplyClaimLock(builder, false)
	if err != nil {
		return actionapplication.WorkspaceCommercialConfiguration{}, fmt.Errorf("apply Workspace commercial configuration lock: %w", err)
	}
	statement, arguments, err := builder.Build()
	if err != nil {
		return actionapplication.WorkspaceCommercialConfiguration{}, err
	}
	var result actionapplication.WorkspaceCommercialConfiguration
	if err := executor.QueryRowContext(ctx, statement, arguments...).Scan(&result.MaxStores, &result.Revision); errors.Is(err, sql.ErrNoRows) {
		return actionapplication.WorkspaceCommercialConfiguration{}, fmt.Errorf("Workspace commercial configuration is missing")
	} else if err != nil {
		return actionapplication.WorkspaceCommercialConfiguration{}, err
	}
	if result.MaxStores < 1 || result.Revision < 1 {
		return actionapplication.WorkspaceCommercialConfiguration{}, fmt.Errorf("Workspace commercial configuration is invalid")
	}
	receiptBuilder := query.NewSelectBuilder(store.runtime.RuntimeRenderer(), "_workspace_provisioning_receipts_v3").
		Columns("company_id").
		Where(query.And(query.Equal("workspace_id", workspaceID), query.Equal("receipt_status", "committed")))
	receiptStatement, receiptArguments, err := receiptBuilder.Build()
	if err != nil {
		return actionapplication.WorkspaceCommercialConfiguration{}, err
	}
	rows, err := executor.QueryContext(ctx, receiptStatement, receiptArguments...)
	if err != nil {
		return actionapplication.WorkspaceCommercialConfiguration{}, fmt.Errorf("resolve Workspace company authority: %w", err)
	}
	defer rows.Close()
	companyIDs := map[string]struct{}{}
	for rows.Next() {
		var companyID sql.NullString
		if err := rows.Scan(&companyID); err != nil {
			return actionapplication.WorkspaceCommercialConfiguration{}, err
		}
		if candidate := strings.TrimSpace(companyID.String); companyID.Valid && candidate != "" {
			companyIDs[candidate] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return actionapplication.WorkspaceCommercialConfiguration{}, err
	}
	if len(companyIDs) != 1 {
		return actionapplication.WorkspaceCommercialConfiguration{}, fmt.Errorf("Workspace company authority is missing or ambiguous")
	}
	for companyID := range companyIDs {
		result.CompanyOrganizationID = companyID
	}
	return result, nil
}

var _ actionapplication.WorkspaceCommercialConfigurationLocker = (*CommercialConfigurationStore)(nil)
