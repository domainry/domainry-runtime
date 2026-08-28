package integration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"time"

	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
)

type lifecycleSQLStore interface {
	DB() *sql.DB
	Identifier(string) string
	TableIdentifier(string) string
	Placeholder(int) string
}

type IntegrationSubjectLifecycleStore struct{ store lifecycleSQLStore }

func NewIntegrationSubjectLifecycleStore(store lifecycleSQLStore) *IntegrationSubjectLifecycleStore {
	return &IntegrationSubjectLifecycleStore{store: store}
}

func (s *IntegrationSubjectLifecycleStore) Owner(context.Context) string { return "integration" }

func (s *IntegrationSubjectLifecycleStore) PreviewSubject(ctx context.Context, workspaceID, identity string) (json.RawMessage, error) {
	var count int64
	query := "SELECT COUNT(*) FROM " + s.store.TableIdentifier("integration_external_identities") + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(1) + " AND " + s.store.Identifier("actor_id") + " = " + s.store.Placeholder(2)
	if err := s.store.DB().QueryRowContext(ctx, query, workspaceID, identity).Scan(&count); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]int64{"external_identity_mappings": count})
}

func (s *IntegrationSubjectLifecycleStore) ExportSubject(ctx context.Context, workspaceID, identity string) (json.RawMessage, error) {
	query := "SELECT " + lifecycleIntegrationColumns(s.store, "provider", "external_subject", "external_subject_type", "external_name", "external_organization", "external_department", "external_group", "status") + " FROM " + s.store.TableIdentifier("integration_external_identities") + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(1) + " AND " + s.store.Identifier("actor_id") + " = " + s.store.Placeholder(2)
	rows, err := s.store.DB().QueryContext(ctx, query, workspaceID, identity)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]string{}
	for rows.Next() {
		var provider, subject, subjectType, name, organization, department, group, status string
		if err := rows.Scan(&provider, &subject, &subjectType, &name, &organization, &department, &group, &status); err != nil {
			return nil, err
		}
		items = append(items, map[string]string{"provider": provider, "external_subject": subject, "external_subject_type": subjectType, "external_name": name, "external_organization": organization, "external_department": department, "external_group": group, "status": status})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return json.Marshal(items)
}

func (s *IntegrationSubjectLifecycleStore) EraseSubject(ctx context.Context, workspaceID, identity string, _ []lifecyclemodel.LegalHold) (json.RawMessage, error) {
	anonymous := integrationAnonymousSubject(workspaceID, identity)
	query := "UPDATE " + s.store.TableIdentifier("integration_external_identities") + " SET " + s.store.Identifier("external_subject") + " = " + s.store.Placeholder(1) + ", " + s.store.Identifier("external_name") + " = '', " + s.store.Identifier("external_organization") + " = '', " + s.store.Identifier("external_department") + " = '', " + s.store.Identifier("external_group") + " = '', " + s.store.Identifier("external_bot_id") + " = '', " + s.store.Identifier("status") + " = 'erased', " + s.store.Identifier("updated_at") + " = " + s.store.Placeholder(2) + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(3) + " AND " + s.store.Identifier("actor_id") + " = " + s.store.Placeholder(4)
	result, err := s.store.DB().ExecContext(ctx, query, anonymous, time.Now().UTC().Format(time.RFC3339Nano), workspaceID, identity)
	if err != nil {
		return nil, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]int64{"anonymized_mappings": changed})
}

func (s *IntegrationSubjectLifecycleStore) RequestExternalErasure(ctx context.Context, request lifecyclemodel.SubjectRequest) ([]lifecyclemodel.ExternalErasure, error) {
	query := "SELECT " + lifecycleIntegrationColumns(s.store, "provider", "external_subject") + " FROM " + s.store.TableIdentifier("integration_external_identities") + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(1) + " AND " + s.store.Identifier("actor_id") + " = " + s.store.Placeholder(2)
	rows, err := s.store.DB().QueryContext(ctx, query, request.WorkspaceID, request.ResolvedIdentity)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []lifecyclemodel.ExternalErasure{}
	for rows.Next() {
		var provider, providerRef string
		if err := rows.Scan(&provider, &providerRef); err != nil {
			return nil, err
		}
		digest := sha256.Sum256([]byte(request.WorkspaceID + "\x00" + request.ID + "\x00" + provider + "\x00" + providerRef))
		result = append(result, lifecyclemodel.ExternalErasure{ID: "external-erasure-" + hex.EncodeToString(digest[:12]), RequestID: request.ID, WorkspaceID: request.WorkspaceID, ConnectorKey: provider, ProviderRef: providerRef, Status: "requested"})
	}
	return result, rows.Err()
}

func integrationAnonymousSubject(workspaceID, identity string) string {
	sum := sha256.Sum256([]byte(workspaceID + "\x00" + identity))
	return "erased-" + hex.EncodeToString(sum[:12])
}

func lifecycleIntegrationColumns(store lifecycleSQLStore, columns ...string) string {
	result := ""
	for index, column := range columns {
		if index > 0 {
			result += ", "
		}
		result += "COALESCE(" + store.Identifier(column) + ", '')"
	}
	return result
}
