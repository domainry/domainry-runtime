package integration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"time"

	lifecyclemodel "github.com/domainry/domainry-lifecycle-sdk/model"
	ormdialect "github.com/domainry/domainry-orm/dialect"
	ormbuilder "github.com/domainry/domainry-orm/query"
)

type lifecycleSQLStore interface {
	DB() *sql.DB
	Identifier(string) string
	TableIdentifier(string) string
	Placeholder(int) string
	RuntimeRenderer() ormdialect.Renderer
}

type IntegrationSubjectLifecycleStore struct{ store lifecycleSQLStore }

func NewIntegrationSubjectLifecycleStore(store lifecycleSQLStore) *IntegrationSubjectLifecycleStore {
	return &IntegrationSubjectLifecycleStore{store: store}
}

func (s *IntegrationSubjectLifecycleStore) Owner(context.Context) string { return "integration" }

func (s *IntegrationSubjectLifecycleStore) PreviewSubject(ctx context.Context, workspaceID, identity string) (json.RawMessage, error) {
	var count int64
	query, args, err := ormbuilder.NewWorkspaceSelectBuilder(s.store.RuntimeRenderer(), "integration_external_identities", workspaceID).
		Projections(ormbuilder.Project(ormbuilder.CountAll())).Where(ormbuilder.Equal("actor_id", identity)).Build()
	if err != nil {
		return nil, err
	}
	if err := s.store.DB().QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]int64{"external_identity_mappings": count})
}

func (s *IntegrationSubjectLifecycleStore) ExportSubject(ctx context.Context, workspaceID, identity string) (json.RawMessage, error) {
	columns := []string{"provider", "external_subject", "external_subject_type", "external_name", "external_organization", "external_department", "external_group", "status"}
	query, args, err := ormbuilder.NewWorkspaceSelectBuilder(s.store.RuntimeRenderer(), "integration_external_identities", workspaceID).Columns(columns...).Where(ormbuilder.Equal("actor_id", identity)).Build()
	if err != nil {
		return nil, err
	}
	rows, err := s.store.DB().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]string{}
	for rows.Next() {
		var provider, subject, subjectType, name, organization, department, group, status sql.NullString
		if err := rows.Scan(&provider, &subject, &subjectType, &name, &organization, &department, &group, &status); err != nil {
			return nil, err
		}
		items = append(items, map[string]string{"provider": provider.String, "external_subject": subject.String, "external_subject_type": subjectType.String, "external_name": name.String, "external_organization": organization.String, "external_department": department.String, "external_group": group.String, "status": status.String})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return json.Marshal(items)
}

func (s *IntegrationSubjectLifecycleStore) EraseSubject(ctx context.Context, workspaceID, identity string, _ []lifecyclemodel.LegalHold) (json.RawMessage, error) {
	anonymous := integrationAnonymousSubject(workspaceID, identity)
	query, args, err := ormbuilder.NewWorkspaceUpdateBuilder(s.store.RuntimeRenderer(), "integration_external_identities", workspaceID).
		Set("external_subject", anonymous).Set("external_name", "").Set("external_organization", "").Set("external_department", "").Set("external_group", "").Set("external_bot_id", "").Set("status", "erased").Set("updated_at", time.Now().UTC().Format(time.RFC3339Nano)).
		Where(ormbuilder.Equal("actor_id", identity)).Build()
	if err != nil {
		return nil, err
	}
	result, err := s.store.DB().ExecContext(ctx, query, args...)
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
	query, args, err := ormbuilder.NewWorkspaceSelectBuilder(s.store.RuntimeRenderer(), "integration_external_identities", request.WorkspaceID).Columns("provider", "external_subject").Where(ormbuilder.Equal("actor_id", request.ResolvedIdentity)).Build()
	if err != nil {
		return nil, err
	}
	rows, err := s.store.DB().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []lifecyclemodel.ExternalErasure{}
	for rows.Next() {
		var providerValue, providerRefValue sql.NullString
		if err := rows.Scan(&providerValue, &providerRefValue); err != nil {
			return nil, err
		}
		provider, providerRef := providerValue.String, providerRefValue.String
		digest := sha256.Sum256([]byte(request.WorkspaceID + "\x00" + request.ID + "\x00" + provider + "\x00" + providerRef))
		result = append(result, lifecyclemodel.ExternalErasure{ID: "external-erasure-" + hex.EncodeToString(digest[:12]), RequestID: request.ID, WorkspaceID: request.WorkspaceID, ConnectorKey: provider, ProviderRef: providerRef, Status: "requested"})
	}
	return result, rows.Err()
}

func integrationAnonymousSubject(workspaceID, identity string) string {
	sum := sha256.Sum256([]byte(workspaceID + "\x00" + identity))
	return "erased-" + hex.EncodeToString(sum[:12])
}
