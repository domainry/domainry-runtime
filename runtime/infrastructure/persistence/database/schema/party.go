package schema

import (
	"context"
	"fmt"
)

func EnsurePartySchema(ctx context.Context, s Store) error {
	text := s.MetadataIDColumnType()
	boolType, boolFalse := "BOOLEAN", "FALSE"
	if s.Driver() == "sqlite" {
		boolType, boolFalse = "INTEGER", "0"
	} else if s.Driver() == "mysql" {
		boolFalse = "0"
	}
	tables := map[string][]string{
		"party_parties": {
			"id " + text + " PRIMARY KEY",
			"workspace_id " + text + " NOT NULL",
			"kind " + text + " NOT NULL",
			"display_name TEXT NOT NULL",
			"status " + text + " NOT NULL",
			"version BIGINT NOT NULL DEFAULT 1",
			"created_at " + text + " NOT NULL",
			"updated_at " + text + " NOT NULL",
		},
		"party_persons": {
			"party_id " + text + " PRIMARY KEY",
			"workspace_id " + text + " NOT NULL",
			"given_name TEXT NOT NULL DEFAULT ''",
			"family_name TEXT NOT NULL DEFAULT ''",
			"birth_date " + text + " NOT NULL DEFAULT ''",
		},
		"party_organizations": {
			"party_id " + text + " PRIMARY KEY",
			"workspace_id " + text + " NOT NULL",
			"legal_name TEXT NOT NULL",
			"registration_number " + text + " NOT NULL DEFAULT ''",
		},
		"party_contact_points": {
			"id " + text + " PRIMARY KEY",
			"workspace_id " + text + " NOT NULL",
			"party_id " + text + " NOT NULL",
			"type " + text + " NOT NULL",
			"value TEXT NOT NULL",
			"label TEXT NOT NULL DEFAULT ''",
			"is_primary " + boolType + " NOT NULL DEFAULT " + boolFalse,
			"verified_at " + text + " NOT NULL DEFAULT ''",
			"status " + text + " NOT NULL",
		},
		"party_addresses": {
			"id " + text + " PRIMARY KEY",
			"workspace_id " + text + " NOT NULL",
			"party_id " + text + " NOT NULL",
			"type " + text + " NOT NULL",
			"line1 TEXT NOT NULL",
			"line2 TEXT NOT NULL DEFAULT ''",
			"locality TEXT NOT NULL DEFAULT ''",
			"region TEXT NOT NULL DEFAULT ''",
			"postal_code " + text + " NOT NULL DEFAULT ''",
			"country " + text + " NOT NULL DEFAULT ''",
			"is_primary " + boolType + " NOT NULL DEFAULT " + boolFalse,
			"status " + text + " NOT NULL",
		},
		"party_identifiers": {
			"id " + text + " PRIMARY KEY",
			"workspace_id " + text + " NOT NULL",
			"party_id " + text + " NOT NULL",
			"type " + text + " NOT NULL",
			"value TEXT NOT NULL",
			"issuer TEXT NOT NULL DEFAULT ''",
			"status " + text + " NOT NULL",
		},
		"party_communication_preferences": {
			"id " + text + " PRIMARY KEY",
			"workspace_id " + text + " NOT NULL",
			"party_id " + text + " NOT NULL",
			"channel " + text + " NOT NULL",
			"allowed " + boolType + " NOT NULL DEFAULT " + boolFalse,
			"preferred " + boolType + " NOT NULL DEFAULT " + boolFalse,
			"locale " + text + " NOT NULL DEFAULT ''",
		},
		"party_consents": {
			"id " + text + " PRIMARY KEY",
			"workspace_id " + text + " NOT NULL",
			"party_id " + text + " NOT NULL",
			"purpose " + text + " NOT NULL",
			"status " + text + " NOT NULL",
			"legal_basis " + text + " NOT NULL DEFAULT ''",
			"source " + text + " NOT NULL",
			"captured_at " + text + " NOT NULL",
			"expires_at " + text + " NOT NULL DEFAULT ''",
			"policy_version " + text + " NOT NULL DEFAULT ''",
		},
		"party_privacy_preferences": {
			"id " + text + " PRIMARY KEY",
			"workspace_id " + text + " NOT NULL",
			"party_id " + text + " NOT NULL",
			"preference_key " + text + " NOT NULL",
			"value TEXT NOT NULL",
			"updated_at " + text + " NOT NULL",
		},
		"party_marketing_subscriptions": {
			"id " + text + " PRIMARY KEY",
			"workspace_id " + text + " NOT NULL",
			"party_id " + text + " NOT NULL",
			"channel " + text + " NOT NULL",
			"topic " + text + " NOT NULL",
			"status " + text + " NOT NULL",
			"contact_point_id " + text + " NOT NULL DEFAULT ''",
			"source " + text + " NOT NULL",
			"subscribed_at " + text + " NOT NULL DEFAULT ''",
			"unsubscribed_at " + text + " NOT NULL DEFAULT ''",
		},
		"party_job_catalog": {
			"id " + text + " PRIMARY KEY",
			"workspace_id " + text + " NOT NULL",
			"code " + text + " NOT NULL",
			"name TEXT NOT NULL",
			"family " + text + " NOT NULL DEFAULT ''",
			"level " + text + " NOT NULL DEFAULT ''",
			"description TEXT NOT NULL DEFAULT ''",
			"status " + text + " NOT NULL",
		},
		"party_positions": {
			"id " + text + " PRIMARY KEY",
			"workspace_id " + text + " NOT NULL",
			"code " + text + " NOT NULL",
			"name TEXT NOT NULL",
			"job_catalog_item_id " + text + " NOT NULL",
			"organization_unit_id " + text + " NOT NULL DEFAULT ''",
			"headcount BIGINT NOT NULL DEFAULT 1",
			"effective_from " + text + " NOT NULL DEFAULT ''",
			"effective_to " + text + " NOT NULL DEFAULT ''",
			"status " + text + " NOT NULL",
		},
		"party_organization_extensions": {
			"id " + text + " PRIMARY KEY",
			"workspace_id " + text + " NOT NULL",
			"kind " + text + " NOT NULL",
			"code " + text + " NOT NULL",
			"name TEXT NOT NULL",
			"claim_value " + text + " NOT NULL DEFAULT ''",
			"parent_id " + text + " NOT NULL DEFAULT ''",
			"organization_unit_id " + text + " NOT NULL DEFAULT ''",
			"status " + text + " NOT NULL",
		},
		"party_organization_extension_memberships": {
			"id " + text + " PRIMARY KEY",
			"workspace_id " + text + " NOT NULL",
			"extension_id " + text + " NOT NULL",
			"workforce_profile_id " + text + " NOT NULL",
			"effective_from " + text + " NOT NULL DEFAULT ''",
			"effective_to " + text + " NOT NULL DEFAULT ''",
			"status " + text + " NOT NULL",
		},
	}
	workspaceIdentities := prepareWorkspaceScopedIdentities(tables)
	for _, table := range sortedRuntimeSchemaTables(tables) {
		if _, err := s.SchemaDB().ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+s.TableIdentifier(table)+" ("+quotedColumnDefinitions(s, tables[table])+")"); err != nil {
			return fmt.Errorf("create %s: %w", table, err)
		}
	}
	if err := ensureWorkspaceScopedIdentities(ctx, s, workspaceIdentities); err != nil {
		return err
	}
	if err := s.EnsureRuntimeColumn(ctx, "party_organization_extensions", "claim_value", text+" NOT NULL DEFAULT ''"); err != nil {
		return fmt.Errorf("prepare party organization extension claim value: %w", err)
	}
	if err := s.CreateIndexIfMissing(ctx, "party_parties", "idx_party_parties_kind", false, "workspace_id", "kind", "status"); err != nil {
		return fmt.Errorf("create party kind index: %w", err)
	}
	for _, table := range []string{
		"party_contact_points", "party_addresses", "party_identifiers", "party_communication_preferences",
		"party_consents", "party_privacy_preferences", "party_marketing_subscriptions",
	} {
		if err := s.CreateIndexIfMissing(ctx, table, "idx_"+table+"_party", false, "workspace_id", "party_id"); err != nil {
			return fmt.Errorf("create %s party index: %w", table, err)
		}
	}
	if err := s.CreateIndexIfMissing(ctx, "party_job_catalog", "idx_party_job_catalog_code", true, "workspace_id", "code"); err != nil {
		return fmt.Errorf("create party job code index: %w", err)
	}
	if err := s.CreateIndexIfMissing(ctx, "party_positions", "idx_party_positions_code", true, "workspace_id", "code"); err != nil {
		return fmt.Errorf("create party position code index: %w", err)
	}
	if err := s.CreateIndexIfMissing(ctx, "party_positions", "idx_party_positions_job", false, "workspace_id", "job_catalog_item_id"); err != nil {
		return fmt.Errorf("create party position job index: %w", err)
	}
	if err := s.CreateIndexIfMissing(ctx, "party_organization_extensions", "idx_party_organization_extension_code", true, "workspace_id", "kind", "code"); err != nil {
		return fmt.Errorf("create party organization extension code index: %w", err)
	}
	if err := s.CreateIndexIfMissing(ctx, "party_organization_extension_memberships", "idx_party_organization_membership_profile", false, "workspace_id", "workforce_profile_id", "status"); err != nil {
		return fmt.Errorf("create party organization membership profile index: %w", err)
	}
	return nil
}
