package artifact

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	foundationartifact "github.com/domainry/domainry-foundation/artifact"
	"github.com/domainry/domainry-orm/query"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

const artifactTable = "_artifacts"
const artifactBindingTable = "_artifact_bindings"

type Store struct{ runtime *database.RuntimeStore }

func NewStore(runtime *database.RuntimeStore) Store { return Store{runtime: runtime} }

func (s Store) executor(ctx context.Context) foundationartifact.Executor {
	return foundationartifact.ExecutorFromContext(ctx, s.runtime.DB())
}

func (s Store) Register(ctx context.Context, value foundationartifact.Artifact) (foundationartifact.Artifact, bool, error) {
	value = normalizeArtifact(value)
	if err := validateArtifact(value); err != nil {
		return foundationartifact.Artifact{}, false, err
	}
	columns := artifactColumns()[1:]
	values := artifactValues(value)[1:]
	statement, arguments, err := query.NewWorkspaceInsertBuilder(s.runtime.SQLRenderer, artifactTable, value.WorkspaceID).Columns(columns...).Values(values...).Build()
	if err != nil {
		return foundationartifact.Artifact{}, false, err
	}
	if _, err = s.executor(ctx).ExecContext(ctx, statement, arguments...); err == nil {
		return value, true, nil
	}
	existing, found, readErr := s.byIdempotency(ctx, value.WorkspaceID, value.Owner, value.Kind, value.IdempotencyKey)
	if readErr != nil {
		return foundationartifact.Artifact{}, false, err
	}
	if !found || !sameArtifactRequest(existing, value) {
		return foundationartifact.Artifact{}, false, foundationartifact.ErrIdentityConflict
	}
	return existing, false, nil
}

func (s Store) ByID(ctx context.Context, workspaceID, id string) (foundationartifact.Artifact, bool, error) {
	return s.find(ctx, workspaceID, query.Equal("id", strings.TrimSpace(id)))
}

func (s Store) ByDownloadTokenHash(ctx context.Context, workspaceID, tokenHash string) (foundationartifact.Artifact, bool, error) {
	return s.find(ctx, workspaceID, query.Equal("download_token_sha256", strings.TrimSpace(tokenHash)))
}

func (s Store) Transition(ctx context.Context, workspaceID, id string, expected, next foundationartifact.Status, scan foundationartifact.ScanStatus, at time.Time) (bool, error) {
	workspaceID, err := artifactWorkspaceID(workspaceID)
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(id) == "" || !artifactStatusValid(expected) || !artifactStatusValid(next) || !artifactScanStatusValid(scan) || at.IsZero() {
		return false, fmt.Errorf("artifact transition is invalid")
	}
	statement, arguments, err := query.NewWorkspaceUpdateBuilder(s.runtime.SQLRenderer, artifactTable, workspaceID).
		Set("status", string(next)).Set("scan_status", string(scan)).Set("updated_at", at.UTC().Format(time.RFC3339Nano)).
		Where(query.And(query.Equal("id", strings.TrimSpace(id)), query.Equal("status", string(expected)))).Build()
	if err != nil {
		return false, err
	}
	result, err := s.executor(ctx).ExecContext(ctx, statement, arguments...)
	if err != nil {
		return false, err
	}
	changed, err := result.RowsAffected()
	return changed == 1, err
}

func (s Store) Bind(ctx context.Context, value foundationartifact.Binding) (foundationartifact.Binding, bool, error) {
	value = normalizeBinding(value)
	if err := validateBinding(value); err != nil {
		return foundationartifact.Binding{}, false, err
	}
	if _, found, err := s.ByID(ctx, value.WorkspaceID, value.ArtifactID); err != nil {
		return foundationartifact.Binding{}, false, err
	} else if !found {
		return foundationartifact.Binding{}, false, fmt.Errorf("artifact binding references an unknown artifact")
	}
	columns := bindingColumns()[1:]
	values := bindingValues(value)[1:]
	statement, arguments, err := query.NewWorkspaceInsertBuilder(s.runtime.SQLRenderer, artifactBindingTable, value.WorkspaceID).Columns(columns...).Values(values...).Build()
	if err != nil {
		return foundationartifact.Binding{}, false, err
	}
	if _, err = s.executor(ctx).ExecContext(ctx, statement, arguments...); err == nil {
		return value, true, nil
	}
	existing, found, readErr := s.bindingByIdentity(ctx, value)
	if readErr != nil {
		return foundationartifact.Binding{}, false, err
	}
	if !found || existing.ArtifactID != value.ArtifactID {
		return foundationartifact.Binding{}, false, foundationartifact.ErrBindingConflict
	}
	return existing, false, nil
}

func (s Store) Bindings(ctx context.Context, workspaceID, artifactID string) ([]foundationartifact.Binding, error) {
	workspaceID, err := artifactWorkspaceID(workspaceID)
	if err != nil {
		return nil, err
	}
	statement, arguments, err := query.NewWorkspaceSelectBuilder(s.runtime.SQLRenderer, artifactBindingTable, workspaceID).
		Columns(bindingColumns()...).Where(query.Equal("artifact_id", strings.TrimSpace(artifactID))).OrderBy(query.Ascending("created_at"), query.Ascending("id")).Build()
	if err != nil {
		return nil, err
	}
	rows, err := s.executor(ctx).QueryContext(ctx, statement, arguments...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []foundationartifact.Binding{}
	for rows.Next() {
		value, scanErr := scanBinding(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (s Store) List(ctx context.Context, workspaceID string, value foundationartifact.Query) ([]foundationartifact.Artifact, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID != "" {
		if _, err := artifactWorkspaceID(workspaceID); err != nil {
			return nil, err
		}
	}
	limit := value.Limit
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	alias := ""
	column := func(name string) query.Expression { return query.Column(name) }
	if value.Binding != nil {
		alias = "artifact"
		column = func(name string) query.Expression { return query.QualifiedColumn(alias, name) }
	}
	predicates := []query.Predicate{}
	if owner := strings.TrimSpace(value.Owner); owner != "" {
		predicates = append(predicates, query.EqualValue(column("owner"), owner))
	}
	if kind := strings.TrimSpace(value.Kind); kind != "" {
		predicates = append(predicates, query.EqualValue(column("kind"), kind))
	}
	if filename := strings.TrimSpace(value.Filename); filename != "" {
		predicates = append(predicates, query.EqualValue(column("filename"), filename))
	}
	if reference := strings.TrimSpace(value.StorageReference); reference != "" {
		predicates = append(predicates, query.EqualValue(column("storage_reference"), reference))
	}
	if !value.ExpiresAtOrBefore.IsZero() {
		expiresAt := value.ExpiresAtOrBefore.UTC().Format(time.RFC3339Nano)
		predicates = append(predicates,
			query.NotEqualValue(column("expires_at"), ""),
			query.LessThanOrEqualValue(column("expires_at"), expiresAt),
		)
	}
	if values := artifactStatusValues(value.Statuses); len(values) > 0 {
		predicates = append(predicates, query.InExpression(column("status"), values...))
	}
	if values := artifactScanStatusValues(value.ScanStatuses); len(values) > 0 {
		predicates = append(predicates, query.InExpression(column("scan_status"), values...))
	}
	ownerPredicates := []query.Predicate{}
	if values := normalizedArtifactStrings(value.CreatedBy); len(values) > 0 {
		ownerPredicates = append(ownerPredicates, query.InExpression(column("created_by"), values...))
	}
	if values := normalizedArtifactStrings(value.OwnerOrgIDs); len(values) > 0 {
		ownerPredicates = append(ownerPredicates, query.InExpression(column("owner_org_id"), values...))
	}
	if len(ownerPredicates) > 0 {
		predicates = append(predicates, query.Or(ownerPredicates...))
	}
	var builder *query.SelectBuilder
	if workspaceID == "" {
		builder = query.NewSelectBuilder(s.runtime.SQLRenderer, artifactTable)
	} else {
		builder = query.NewWorkspaceSelectBuilder(s.runtime.SQLRenderer, artifactTable, workspaceID)
	}
	if value.Binding != nil {
		binding := normalizeArtifactBindingQuery(*value.Binding)
		if binding.Kind == "" || !foundationartifact.BindingKindRegistered(binding.Kind) {
			return nil, fmt.Errorf("artifact binding query kind is invalid")
		}
		builder.Alias(alias).Projections(artifactProjections(alias)...).Join(query.InnerJoin(artifactBindingTable, "binding", query.And(
			query.EqualExpressions(query.QualifiedColumn(alias, "workspace_id"), query.QualifiedColumn("binding", "workspace_id")),
			query.EqualExpressions(query.QualifiedColumn(alias, "id"), query.QualifiedColumn("binding", "artifact_id")),
		)))
		predicates = append(predicates, query.EqualValue(query.QualifiedColumn("binding", "kind"), binding.Kind))
		if binding.Owner != "" {
			predicates = append(predicates, query.EqualValue(query.QualifiedColumn("binding", "owner"), binding.Owner))
		}
		if binding.ResourceType != "" {
			predicates = append(predicates, query.EqualValue(query.QualifiedColumn("binding", "resource_type"), binding.ResourceType))
		}
		if binding.ResourceID != "" {
			predicates = append(predicates, query.EqualValue(query.QualifiedColumn("binding", "resource_id"), binding.ResourceID))
		}
		if binding.FieldKey != "" {
			predicates = append(predicates, query.EqualValue(query.QualifiedColumn("binding", "field_key"), binding.FieldKey))
		}
		if value.NewestFirst {
			builder.OrderBy(query.DescendingExpression(column("created_at")), query.DescendingExpression(column("id"))).Limit(limit)
		} else {
			builder.OrderBy(query.AscendingExpression(column("created_at")), query.AscendingExpression(column("id"))).Limit(limit)
		}
	} else {
		builder.Columns(artifactColumns()...)
		if value.NewestFirst {
			builder.OrderBy(query.Descending("created_at"), query.Descending("id")).Limit(limit)
		} else {
			builder.OrderBy(query.Ascending("created_at"), query.Ascending("id")).Limit(limit)
		}
	}
	if len(predicates) > 0 {
		builder.Where(query.And(predicates...))
	}
	statement, arguments, err := builder.Build()
	if err != nil {
		return nil, err
	}
	rows, err := s.executor(ctx).QueryContext(ctx, statement, arguments...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []foundationartifact.Artifact{}
	for rows.Next() {
		artifact, scanErr := scanArtifact(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, artifact)
	}
	return result, rows.Err()
}

func artifactProjections(alias string) []query.Projection {
	result := make([]query.Projection, 0, len(artifactColumns()))
	for _, name := range artifactColumns() {
		result = append(result, query.Project(query.QualifiedColumn(alias, name)))
	}
	return result
}

func normalizeArtifactBindingQuery(value foundationartifact.BindingQuery) foundationartifact.BindingQuery {
	value.Owner = strings.TrimSpace(value.Owner)
	value.Kind = strings.TrimSpace(value.Kind)
	value.ResourceType = strings.TrimSpace(value.ResourceType)
	value.ResourceID = strings.TrimSpace(value.ResourceID)
	value.FieldKey = strings.TrimSpace(value.FieldKey)
	return value
}

func (s Store) Update(ctx context.Context, value foundationartifact.Mutation) (bool, error) {
	workspaceID, err := artifactWorkspaceID(value.WorkspaceID)
	if err != nil {
		return false, err
	}
	value.ID, value.Owner, value.Kind = strings.TrimSpace(value.ID), strings.TrimSpace(value.Owner), strings.TrimSpace(value.Kind)
	if value.ID == "" || value.Owner == "" || value.Kind == "" || !artifactStatusValid(value.ExpectedStatus) || !artifactStatusValid(value.Status) || !artifactScanStatusValid(value.ExpectedScanStatus) || !artifactScanStatusValid(value.ScanStatus) || value.UpdatedAt.IsZero() || !json.Valid(value.Metadata) {
		return false, fmt.Errorf("artifact mutation is invalid")
	}
	predicates := []query.Predicate{query.Equal("id", value.ID), query.Equal("owner", value.Owner), query.Equal("kind", value.Kind), query.Equal("status", string(value.ExpectedStatus)), query.Equal("scan_status", string(value.ExpectedScanStatus))}
	if !value.ExpectedUpdatedAt.IsZero() {
		predicates = append(predicates, query.Equal("updated_at", value.ExpectedUpdatedAt.UTC().Format(time.RFC3339Nano)))
	}
	statement, arguments, err := query.NewWorkspaceUpdateBuilder(s.runtime.SQLRenderer, artifactTable, workspaceID).
		Set("status", string(value.Status)).Set("scan_status", string(value.ScanStatus)).Set("expires_at", formatOptionalTime(value.ExpiresAt)).
		Set("metadata_json", string(value.Metadata)).Set("updated_at", value.UpdatedAt.UTC().Format(time.RFC3339Nano)).
		Where(query.And(predicates...)).Build()
	if err != nil {
		return false, err
	}
	result, err := s.executor(ctx).ExecContext(ctx, statement, arguments...)
	if err != nil {
		return false, err
	}
	changed, err := result.RowsAffected()
	return changed == 1, err
}

func (s Store) byIdempotency(ctx context.Context, workspaceID, owner, kind, key string) (foundationartifact.Artifact, bool, error) {
	return s.find(ctx, workspaceID, query.And(query.Equal("owner", owner), query.Equal("kind", kind), query.Equal("idempotency_key", key)))
}

func (s Store) find(ctx context.Context, workspaceID string, predicate query.Predicate) (foundationartifact.Artifact, bool, error) {
	workspaceID, err := artifactWorkspaceID(workspaceID)
	if err != nil {
		return foundationartifact.Artifact{}, false, err
	}
	statement, arguments, err := query.NewWorkspaceSelectBuilder(s.runtime.SQLRenderer, artifactTable, workspaceID).Columns(artifactColumns()...).Where(predicate).Limit(1).Build()
	if err != nil {
		return foundationartifact.Artifact{}, false, err
	}
	value, err := scanArtifact(s.executor(ctx).QueryRowContext(ctx, statement, arguments...))
	if errors.Is(err, sql.ErrNoRows) {
		return foundationartifact.Artifact{}, false, nil
	}
	return value, err == nil, err
}

func (s Store) bindingByIdentity(ctx context.Context, value foundationartifact.Binding) (foundationartifact.Binding, bool, error) {
	predicate := query.And(query.Equal("artifact_id", value.ArtifactID), query.Equal("owner", value.Owner), query.Equal("kind", value.Kind), query.Equal("resource_type", value.ResourceType), query.Equal("resource_id", value.ResourceID), query.Equal("field_key", value.FieldKey))
	statement, arguments, err := query.NewWorkspaceSelectBuilder(s.runtime.SQLRenderer, artifactBindingTable, value.WorkspaceID).Columns(bindingColumns()...).Where(predicate).Limit(1).Build()
	if err != nil {
		return foundationartifact.Binding{}, false, err
	}
	existing, err := scanBinding(s.executor(ctx).QueryRowContext(ctx, statement, arguments...))
	if errors.Is(err, sql.ErrNoRows) {
		return foundationartifact.Binding{}, false, nil
	}
	return existing, err == nil, err
}

func artifactColumns() []string {
	return []string{"workspace_id", "id", "owner", "kind", "idempotency_key", "created_by", "owner_org_id", "filename", "media_type", "content_sha256", "size_bytes", "storage_reference", "status", "expires_at", "scan_status", "download_token_sha256", "authorization_scope_sha256", "metadata_json", "created_at", "updated_at"}
}

func artifactValues(value foundationartifact.Artifact) []any {
	return []any{value.WorkspaceID, value.ID, value.Owner, value.Kind, value.IdempotencyKey, value.CreatedBy, value.OwnerOrgID, value.Filename, value.MediaType, value.ContentSHA256, value.SizeBytes, value.StorageReference, string(value.Status), formatOptionalTime(value.ExpiresAt), string(value.ScanStatus), value.DownloadTokenSHA256, value.AuthorizationScopeSHA256, string(value.Metadata), value.CreatedAt.UTC().Format(time.RFC3339Nano), value.UpdatedAt.UTC().Format(time.RFC3339Nano)}
}

func bindingColumns() []string {
	return []string{"workspace_id", "id", "artifact_id", "owner", "kind", "resource_type", "resource_id", "field_key", "metadata_json", "created_at"}
}

func bindingValues(value foundationartifact.Binding) []any {
	return []any{value.WorkspaceID, value.ID, value.ArtifactID, value.Owner, value.Kind, value.ResourceType, value.ResourceID, value.FieldKey, string(value.Metadata), value.CreatedAt.UTC().Format(time.RFC3339Nano)}
}

type scanner interface{ Scan(...any) error }

func scanArtifact(row scanner) (foundationartifact.Artifact, error) {
	var value foundationartifact.Artifact
	var status, scanStatus, metadata, createdAt, updatedAt, expiresAt string
	err := row.Scan(&value.WorkspaceID, &value.ID, &value.Owner, &value.Kind, &value.IdempotencyKey, &value.CreatedBy, &value.OwnerOrgID, &value.Filename, &value.MediaType, &value.ContentSHA256, &value.SizeBytes, &value.StorageReference, &status, &expiresAt, &scanStatus, &value.DownloadTokenSHA256, &value.AuthorizationScopeSHA256, &metadata, &createdAt, &updatedAt)
	if err != nil {
		return value, err
	}
	value.Status, value.ScanStatus, value.Metadata = foundationartifact.Status(status), foundationartifact.ScanStatus(scanStatus), json.RawMessage(metadata)
	if value.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
		return value, err
	}
	if value.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt); err != nil {
		return value, err
	}
	if expiresAt != "" {
		value.ExpiresAt, err = time.Parse(time.RFC3339Nano, expiresAt)
	}
	return value, err
}

func scanBinding(row scanner) (foundationartifact.Binding, error) {
	var value foundationartifact.Binding
	var metadata, createdAt string
	err := row.Scan(&value.WorkspaceID, &value.ID, &value.ArtifactID, &value.Owner, &value.Kind, &value.ResourceType, &value.ResourceID, &value.FieldKey, &metadata, &createdAt)
	if err != nil {
		return value, err
	}
	value.Metadata = json.RawMessage(metadata)
	value.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	return value, err
}

func normalizeArtifact(value foundationartifact.Artifact) foundationartifact.Artifact {
	value.ID, value.WorkspaceID, value.Owner, value.Kind = strings.TrimSpace(value.ID), strings.TrimSpace(value.WorkspaceID), strings.TrimSpace(value.Owner), strings.TrimSpace(value.Kind)
	value.IdempotencyKey, value.CreatedBy = strings.TrimSpace(value.IdempotencyKey), strings.TrimSpace(value.CreatedBy)
	value.OwnerOrgID = strings.TrimSpace(value.OwnerOrgID)
	value.Filename, value.MediaType = strings.TrimSpace(value.Filename), strings.TrimSpace(value.MediaType)
	value.ContentSHA256, value.StorageReference = strings.TrimSpace(value.ContentSHA256), strings.TrimSpace(value.StorageReference)
	value.DownloadTokenSHA256, value.AuthorizationScopeSHA256 = strings.TrimSpace(value.DownloadTokenSHA256), strings.TrimSpace(value.AuthorizationScopeSHA256)
	if len(bytes.TrimSpace(value.Metadata)) == 0 {
		value.Metadata = json.RawMessage(`{}`)
	}
	return value
}

func validateArtifact(value foundationartifact.Artifact) error {
	if _, err := principalmodel.NewWorkspaceID(value.WorkspaceID); err != nil {
		return err
	}
	if !artifactOwnerKindRegistered(value.Owner, value.Kind) {
		return fmt.Errorf("artifact owner and kind are not registered")
	}
	if value.ID == "" || value.IdempotencyKey == "" || value.CreatedBy == "" || value.Filename == "" || value.MediaType == "" || value.ContentSHA256 == "" || value.StorageReference == "" || value.SizeBytes < 0 {
		return fmt.Errorf("artifact identity and content metadata are required")
	}
	if !artifactStatusValid(value.Status) || !artifactScanStatusValid(value.ScanStatus) || value.CreatedAt.IsZero() || !value.UpdatedAt.Equal(value.CreatedAt) || !json.Valid(value.Metadata) {
		return fmt.Errorf("artifact state is invalid")
	}
	return nil
}

func artifactOwnerKindRegistered(owner, kind string) bool {
	if _, found := foundationartifact.RegistrationFor(owner, kind); found {
		return true
	}
	// Runtime owns this bounded result-artifact kind; the shared Foundation
	// registry exposes the same closed owner/kind contract to module hosts.
	return strings.TrimSpace(owner) == "operations" && strings.TrimSpace(kind) == "result"
}

func normalizeBinding(value foundationartifact.Binding) foundationartifact.Binding {
	value.ID, value.WorkspaceID, value.ArtifactID = strings.TrimSpace(value.ID), strings.TrimSpace(value.WorkspaceID), strings.TrimSpace(value.ArtifactID)
	value.Owner, value.Kind = strings.TrimSpace(value.Owner), strings.TrimSpace(value.Kind)
	value.ResourceType, value.ResourceID, value.FieldKey = strings.TrimSpace(value.ResourceType), strings.TrimSpace(value.ResourceID), strings.TrimSpace(value.FieldKey)
	if len(bytes.TrimSpace(value.Metadata)) == 0 {
		value.Metadata = json.RawMessage(`{}`)
	}
	return value
}

func validateBinding(value foundationartifact.Binding) error {
	if _, err := principalmodel.NewWorkspaceID(value.WorkspaceID); err != nil {
		return err
	}
	if value.ID == "" || value.ArtifactID == "" || value.Owner == "" || !foundationartifact.BindingKindRegistered(value.Kind) || value.ResourceType == "" || value.ResourceID == "" || value.CreatedAt.IsZero() || !json.Valid(value.Metadata) {
		return fmt.Errorf("artifact binding is invalid")
	}
	return nil
}

func sameArtifactRequest(left, right foundationartifact.Artifact) bool {
	return left.Owner == right.Owner && left.Kind == right.Kind && left.IdempotencyKey == right.IdempotencyKey && left.CreatedBy == right.CreatedBy && left.OwnerOrgID == right.OwnerOrgID && left.Filename == right.Filename && left.MediaType == right.MediaType && left.ContentSHA256 == right.ContentSHA256 && left.SizeBytes == right.SizeBytes && left.StorageReference == right.StorageReference && left.DownloadTokenSHA256 == right.DownloadTokenSHA256 && left.AuthorizationScopeSHA256 == right.AuthorizationScopeSHA256 && left.ExpiresAt.Equal(right.ExpiresAt) && bytes.Equal(left.Metadata, right.Metadata)
}

func artifactWorkspaceID(value string) (string, error) {
	workspaceID, err := principalmodel.NewWorkspaceID(value)
	if err != nil {
		return "", err
	}
	return workspaceID.String(), nil
}

func artifactStatusValid(value foundationartifact.Status) bool {
	switch value {
	case foundationartifact.StatusPending, foundationartifact.StatusAvailable, foundationartifact.StatusRejected, foundationartifact.StatusExpired, foundationartifact.StatusDeleted:
		return true
	default:
		return false
	}
}

func artifactScanStatusValid(value foundationartifact.ScanStatus) bool {
	switch value {
	case foundationartifact.ScanNotRequired, foundationartifact.ScanPending, foundationartifact.ScanClean, foundationartifact.ScanRejected, foundationartifact.ScanFailed:
		return true
	default:
		return false
	}
}

func artifactStatusValues(values []foundationartifact.Status) []any {
	result := make([]any, 0, len(values))
	for _, value := range values {
		if artifactStatusValid(value) {
			result = append(result, string(value))
		}
	}
	return result
}

func artifactScanStatusValues(values []foundationartifact.ScanStatus) []any {
	result := make([]any, 0, len(values))
	for _, value := range values {
		if artifactScanStatusValid(value) {
			result = append(result, string(value))
		}
	}
	return result
}

func normalizedArtifactStrings(values []string) []any {
	seen := map[string]bool{}
	result := make([]any, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func formatOptionalTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

var _ foundationartifact.ManagedStore = Store{}
