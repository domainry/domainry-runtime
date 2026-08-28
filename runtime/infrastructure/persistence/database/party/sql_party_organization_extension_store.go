package party

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	partymodel "github.com/domainry/domainry-runtime/runtime/domain/party/model"
)

func (s *SQLPartyStore) ListOrganizationExtensions(ctx context.Context, workspaceID string) ([]partymodel.OrganizationExtension, error) {
	if err := requireWorkspace(workspaceID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+s.columns("id", "kind", "code", "name", "claim_value", "parent_id", "organization_unit_id", "status")+" FROM "+s.table("party_organization_extensions")+" WHERE "+s.identifier("workspace_id")+" = "+s.placeholder(1), workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list party organization extensions: %w", err)
	}
	defer rows.Close()
	values := []partymodel.OrganizationExtension{}
	for rows.Next() {
		value := partymodel.OrganizationExtension{}
		if err := rows.Scan(&value.ID, &value.Kind, &value.Code, &value.Name, &value.ClaimValue, &value.ParentID, &value.OrganizationUnitID, &value.Status); err != nil {
			return nil, fmt.Errorf("scan party organization extension: %w", err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate party organization extensions: %w", err)
	}
	sort.Slice(values, func(left, right int) bool { return values[left].ID < values[right].ID })
	return values, nil
}

func (s *SQLPartyStore) ResolveIdentityOrganizationScopes(ctx context.Context, workspaceID string, workforceProfileIDs []string) (partymodel.OrganizationScopeFacts, error) {
	if err := requireWorkspace(workspaceID); err != nil {
		return partymodel.OrganizationScopeFacts{}, err
	}
	facts := partymodel.OrganizationScopeFacts{}
	seen := map[string]map[string]bool{
		partymodel.OrganizationExtensionTeam: {}, partymodel.OrganizationExtensionStore: {},
		partymodel.OrganizationExtensionTerritory: {}, partymodel.OrganizationExtensionWarehouse: {},
	}
	profileSet := map[string]bool{}
	for _, profileID := range workforceProfileIDs {
		if profileID = strings.TrimSpace(profileID); profileID != "" {
			profileSet[profileID] = true
		}
	}
	profileIDs := make([]string, 0, len(profileSet))
	for profileID := range profileSet {
		profileIDs = append(profileIDs, profileID)
	}
	sort.Strings(profileIDs)
	if len(profileIDs) == 0 {
		return facts, nil
	}
	args := []any{workspaceID}
	placeholders := make([]string, 0, len(profileIDs))
	for _, profileID := range profileIDs {
		args = append(args, profileID)
		placeholders = append(placeholders, s.placeholder(len(args)))
	}
	membershipTable, extensionTable := s.table("party_organization_extension_memberships"), s.table("party_organization_extensions")
	query := "SELECT e." + s.identifier("kind") + ", e." + s.identifier("claim_value") + ", e." + s.identifier("id") +
		", m." + s.identifier("effective_from") + ", m." + s.identifier("effective_to") + ", m." + s.identifier("status") + ", e." + s.identifier("status") +
		" FROM " + membershipTable + " m JOIN " + extensionTable + " e ON e." + s.identifier("workspace_id") + " = m." + s.identifier("workspace_id") + " AND e." + s.identifier("id") + " = m." + s.identifier("extension_id") +
		" WHERE m." + s.identifier("workspace_id") + " = " + s.placeholder(1) + " AND m." + s.identifier("workforce_profile_id") + " IN (" + strings.Join(placeholders, ", ") + ")"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return partymodel.OrganizationScopeFacts{}, fmt.Errorf("resolve party organization scopes: %w", err)
	}
	defer rows.Close()
	now := time.Now().UTC()
	for rows.Next() {
		var kind, claimValue, extensionID, effectiveFrom, effectiveTo, membershipStatus, extensionStatus string
		if err := rows.Scan(&kind, &claimValue, &extensionID, &effectiveFrom, &effectiveTo, &membershipStatus, &extensionStatus); err != nil {
			return partymodel.OrganizationScopeFacts{}, fmt.Errorf("scan party organization scope: %w", err)
		}
		membership := partymodel.OrganizationExtensionMembership{EffectiveFrom: effectiveFrom, EffectiveTo: effectiveTo, Status: membershipStatus}
		kindIDs, knownKind := seen[kind]
		if membershipStatus != partymodel.PartyStatusActive || extensionStatus != partymodel.PartyStatusActive || !knownKind || !organizationMembershipEffective(membership, now) {
			continue
		}
		if claimValue = strings.TrimSpace(claimValue); claimValue == "" {
			claimValue = extensionID
		}
		kindIDs[claimValue] = true
	}
	if err := rows.Err(); err != nil {
		return partymodel.OrganizationScopeFacts{}, fmt.Errorf("iterate party organization scopes: %w", err)
	}
	facts.TeamIDs = sortedOrganizationScopeIDs(seen[partymodel.OrganizationExtensionTeam])
	facts.StoreIDs = sortedOrganizationScopeIDs(seen[partymodel.OrganizationExtensionStore])
	facts.TerritoryIDs = sortedOrganizationScopeIDs(seen[partymodel.OrganizationExtensionTerritory])
	facts.WarehouseIDs = sortedOrganizationScopeIDs(seen[partymodel.OrganizationExtensionWarehouse])
	return facts, nil
}

func organizationMembershipEffective(value partymodel.OrganizationExtensionMembership, now time.Time) bool {
	if value.EffectiveFrom != "" {
		if parsed, err := time.Parse(time.RFC3339, value.EffectiveFrom); err == nil && now.Before(parsed) {
			return false
		}
	}
	if value.EffectiveTo != "" {
		if parsed, err := time.Parse(time.RFC3339, value.EffectiveTo); err == nil && !now.Before(parsed) {
			return false
		}
	}
	return true
}

func sortedOrganizationScopeIDs(values map[string]bool) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func (s *SQLPartyStore) GetOrganizationExtension(ctx context.Context, workspaceID, id string) (partymodel.OrganizationExtension, bool, error) {
	if err := requireWorkspace(workspaceID); err != nil {
		return partymodel.OrganizationExtension{}, false, err
	}
	value := partymodel.OrganizationExtension{}
	err := s.db.QueryRowContext(ctx, "SELECT "+s.columns("id", "kind", "code", "name", "claim_value", "parent_id", "organization_unit_id", "status")+" FROM "+s.table("party_organization_extensions")+" WHERE "+s.identifier("workspace_id")+" = "+s.placeholder(1)+" AND "+s.identifier("id")+" = "+s.placeholder(2), workspaceID, strings.TrimSpace(id)).
		Scan(&value.ID, &value.Kind, &value.Code, &value.Name, &value.ClaimValue, &value.ParentID, &value.OrganizationUnitID, &value.Status)
	if err == sql.ErrNoRows {
		return partymodel.OrganizationExtension{}, false, nil
	}
	if err != nil {
		return partymodel.OrganizationExtension{}, false, fmt.Errorf("get party organization extension: %w", err)
	}
	return value, true, nil
}

func (s *SQLPartyStore) UpsertOrganizationExtension(ctx context.Context, workspaceID string, value partymodel.OrganizationExtension) (partymodel.OrganizationExtension, error) {
	if err := requireWorkspace(workspaceID); err != nil {
		return partymodel.OrganizationExtension{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return partymodel.OrganizationExtension{}, fmt.Errorf("begin party organization extension upsert: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, "DELETE FROM "+s.table("party_organization_extensions")+" WHERE "+s.identifier("workspace_id")+" = "+s.placeholder(1)+" AND "+s.identifier("id")+" = "+s.placeholder(2), workspaceID, value.ID); err != nil {
		return partymodel.OrganizationExtension{}, fmt.Errorf("replace party organization extension: %w", err)
	}
	query := "INSERT INTO " + s.table("party_organization_extensions") + " (" + s.columns("id", "workspace_id", "kind", "code", "name", "claim_value", "parent_id", "organization_unit_id", "status") + ") VALUES (" + s.placeholders(9) + ")"
	if _, err := tx.ExecContext(ctx, query, value.ID, workspaceID, value.Kind, value.Code, value.Name, value.ClaimValue, value.ParentID, value.OrganizationUnitID, value.Status); err != nil {
		return partymodel.OrganizationExtension{}, fmt.Errorf("write party organization extension: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return partymodel.OrganizationExtension{}, fmt.Errorf("commit party organization extension upsert: %w", err)
	}
	return value, nil
}

func (s *SQLPartyStore) ListOrganizationExtensionMemberships(ctx context.Context, workspaceID, workforceProfileID string) ([]partymodel.OrganizationExtensionMembership, error) {
	if err := requireWorkspace(workspaceID); err != nil {
		return nil, err
	}
	query := "SELECT " + s.columns("id", "extension_id", "workforce_profile_id", "effective_from", "effective_to", "status") +
		" FROM " + s.table("party_organization_extension_memberships") + " WHERE " + s.identifier("workspace_id") + " = " + s.placeholder(1)
	args := []any{workspaceID}
	if workforceProfileID = strings.TrimSpace(workforceProfileID); workforceProfileID != "" {
		query += " AND " + s.identifier("workforce_profile_id") + " = " + s.placeholder(2)
		args = append(args, workforceProfileID)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list party organization memberships: %w", err)
	}
	defer rows.Close()
	values := []partymodel.OrganizationExtensionMembership{}
	for rows.Next() {
		value := partymodel.OrganizationExtensionMembership{}
		if err := rows.Scan(&value.ID, &value.ExtensionID, &value.WorkforceProfileID, &value.EffectiveFrom, &value.EffectiveTo, &value.Status); err != nil {
			return nil, fmt.Errorf("scan party organization membership: %w", err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate party organization memberships: %w", err)
	}
	sort.Slice(values, func(left, right int) bool { return values[left].ID < values[right].ID })
	return values, nil
}

func (s *SQLPartyStore) UpsertOrganizationExtensionMembership(ctx context.Context, workspaceID string, value partymodel.OrganizationExtensionMembership) (partymodel.OrganizationExtensionMembership, error) {
	if err := requireWorkspace(workspaceID); err != nil {
		return partymodel.OrganizationExtensionMembership{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return partymodel.OrganizationExtensionMembership{}, fmt.Errorf("begin party organization membership upsert: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, "DELETE FROM "+s.table("party_organization_extension_memberships")+" WHERE "+s.identifier("workspace_id")+" = "+s.placeholder(1)+" AND "+s.identifier("id")+" = "+s.placeholder(2), workspaceID, value.ID); err != nil {
		return partymodel.OrganizationExtensionMembership{}, fmt.Errorf("replace party organization membership: %w", err)
	}
	query := "INSERT INTO " + s.table("party_organization_extension_memberships") + " (" + s.columns("id", "workspace_id", "extension_id", "workforce_profile_id", "effective_from", "effective_to", "status") + ") VALUES (" + s.placeholders(7) + ")"
	if _, err := tx.ExecContext(ctx, query, value.ID, workspaceID, value.ExtensionID, value.WorkforceProfileID, value.EffectiveFrom, value.EffectiveTo, value.Status); err != nil {
		return partymodel.OrganizationExtensionMembership{}, fmt.Errorf("write party organization membership: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return partymodel.OrganizationExtensionMembership{}, fmt.Errorf("commit party organization membership upsert: %w", err)
	}
	return value, nil
}
