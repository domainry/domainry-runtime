package party

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	partymodel "github.com/domainry/domainry-runtime/runtime/domain/party/model"
	partyrepository "github.com/domainry/domainry-runtime/runtime/domain/party/repository"
)

var _ partyrepository.PartyCatalogRepository = (*SQLPartyStore)(nil)

func (s *SQLPartyStore) ListJobs(ctx context.Context, workspaceID string) ([]partymodel.JobCatalogItem, error) {
	if err := requireWorkspace(workspaceID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+s.columns("id", "code", "name", "family", "level", "description", "status")+" FROM "+s.table("party_job_catalog")+" WHERE "+s.identifier("workspace_id")+" = "+s.placeholder(1), workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list party jobs: %w", err)
	}
	defer rows.Close()
	values := []partymodel.JobCatalogItem{}
	for rows.Next() {
		value := partymodel.JobCatalogItem{}
		if err := rows.Scan(&value.ID, &value.Code, &value.Name, &value.Family, &value.Level, &value.Description, &value.Status); err != nil {
			return nil, fmt.Errorf("scan party job: %w", err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate party jobs: %w", err)
	}
	sort.Slice(values, func(left, right int) bool { return values[left].ID < values[right].ID })
	return values, nil
}

func (s *SQLPartyStore) GetJob(ctx context.Context, workspaceID, id string) (partymodel.JobCatalogItem, bool, error) {
	if err := requireWorkspace(workspaceID); err != nil {
		return partymodel.JobCatalogItem{}, false, err
	}
	value := partymodel.JobCatalogItem{}
	err := s.db.QueryRowContext(ctx, "SELECT "+s.columns("id", "code", "name", "family", "level", "description", "status")+" FROM "+s.table("party_job_catalog")+" WHERE "+s.identifier("workspace_id")+" = "+s.placeholder(1)+" AND "+s.identifier("id")+" = "+s.placeholder(2), workspaceID, strings.TrimSpace(id)).
		Scan(&value.ID, &value.Code, &value.Name, &value.Family, &value.Level, &value.Description, &value.Status)
	if err == sql.ErrNoRows {
		return partymodel.JobCatalogItem{}, false, nil
	}
	if err != nil {
		return partymodel.JobCatalogItem{}, false, fmt.Errorf("get party job: %w", err)
	}
	return value, true, nil
}

func (s *SQLPartyStore) UpsertJob(ctx context.Context, workspaceID string, value partymodel.JobCatalogItem) (partymodel.JobCatalogItem, error) {
	if err := requireWorkspace(workspaceID); err != nil {
		return partymodel.JobCatalogItem{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return partymodel.JobCatalogItem{}, fmt.Errorf("begin party job upsert: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, "DELETE FROM "+s.table("party_job_catalog")+" WHERE "+s.identifier("workspace_id")+" = "+s.placeholder(1)+" AND "+s.identifier("id")+" = "+s.placeholder(2), workspaceID, value.ID); err != nil {
		return partymodel.JobCatalogItem{}, fmt.Errorf("replace party job: %w", err)
	}
	query := "INSERT INTO " + s.table("party_job_catalog") + " (" + s.columns("id", "workspace_id", "code", "name", "family", "level", "description", "status") + ") VALUES (" + s.placeholders(8) + ")"
	if _, err := tx.ExecContext(ctx, query, value.ID, workspaceID, value.Code, value.Name, value.Family, value.Level, value.Description, value.Status); err != nil {
		return partymodel.JobCatalogItem{}, fmt.Errorf("write party job: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return partymodel.JobCatalogItem{}, fmt.Errorf("commit party job upsert: %w", err)
	}
	return value, nil
}

func (s *SQLPartyStore) ListPositions(ctx context.Context, workspaceID string) ([]partymodel.Position, error) {
	if err := requireWorkspace(workspaceID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+s.columns("id", "code", "name", "job_catalog_item_id", "organization_unit_id", "headcount", "effective_from", "effective_to", "status")+" FROM "+s.table("party_positions")+" WHERE "+s.identifier("workspace_id")+" = "+s.placeholder(1), workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list party positions: %w", err)
	}
	defer rows.Close()
	values := []partymodel.Position{}
	for rows.Next() {
		value := partymodel.Position{}
		if err := rows.Scan(&value.ID, &value.Code, &value.Name, &value.JobCatalogItemID, &value.OrganizationUnitID, &value.Headcount, &value.EffectiveFrom, &value.EffectiveTo, &value.Status); err != nil {
			return nil, fmt.Errorf("scan party position: %w", err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate party positions: %w", err)
	}
	sort.Slice(values, func(left, right int) bool { return values[left].ID < values[right].ID })
	return values, nil
}

func (s *SQLPartyStore) GetPosition(ctx context.Context, workspaceID, id string) (partymodel.Position, bool, error) {
	if err := requireWorkspace(workspaceID); err != nil {
		return partymodel.Position{}, false, err
	}
	value := partymodel.Position{}
	err := s.db.QueryRowContext(ctx, "SELECT "+s.columns("id", "code", "name", "job_catalog_item_id", "organization_unit_id", "headcount", "effective_from", "effective_to", "status")+" FROM "+s.table("party_positions")+" WHERE "+s.identifier("workspace_id")+" = "+s.placeholder(1)+" AND "+s.identifier("id")+" = "+s.placeholder(2), workspaceID, strings.TrimSpace(id)).
		Scan(&value.ID, &value.Code, &value.Name, &value.JobCatalogItemID, &value.OrganizationUnitID, &value.Headcount, &value.EffectiveFrom, &value.EffectiveTo, &value.Status)
	if err == sql.ErrNoRows {
		return partymodel.Position{}, false, nil
	}
	if err != nil {
		return partymodel.Position{}, false, fmt.Errorf("get party position: %w", err)
	}
	return value, true, nil
}

func (s *SQLPartyStore) UpsertPosition(ctx context.Context, workspaceID string, value partymodel.Position) (partymodel.Position, error) {
	if err := requireWorkspace(workspaceID); err != nil {
		return partymodel.Position{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return partymodel.Position{}, fmt.Errorf("begin party position upsert: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, "DELETE FROM "+s.table("party_positions")+" WHERE "+s.identifier("workspace_id")+" = "+s.placeholder(1)+" AND "+s.identifier("id")+" = "+s.placeholder(2), workspaceID, value.ID); err != nil {
		return partymodel.Position{}, fmt.Errorf("replace party position: %w", err)
	}
	query := "INSERT INTO " + s.table("party_positions") + " (" + s.columns("id", "workspace_id", "code", "name", "job_catalog_item_id", "organization_unit_id", "headcount", "effective_from", "effective_to", "status") + ") VALUES (" + s.placeholders(10) + ")"
	if _, err := tx.ExecContext(ctx, query, value.ID, workspaceID, value.Code, value.Name, value.JobCatalogItemID, value.OrganizationUnitID, value.Headcount, value.EffectiveFrom, value.EffectiveTo, value.Status); err != nil {
		return partymodel.Position{}, fmt.Errorf("write party position: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return partymodel.Position{}, fmt.Errorf("commit party position upsert: %w", err)
	}
	return value, nil
}
