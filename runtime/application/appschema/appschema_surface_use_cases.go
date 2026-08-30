package appschema

import (
	"context"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

const PermissionMetadataOpsRead = "metadata.ops.read"

type PublishedRuntimeSchemaDTO struct {
	TemplateID      string                                                 `json:"template_id"`
	TemplateVersion string                                                 `json:"template_version"`
	Name            string                                                 `json:"name,omitempty"`
	SchemaHash      string                                                 `json:"schema_hash"`
	SnapshotVersion string                                                 `json:"snapshot_version"`
	Objects         []definitionmodel.ObjectSchema                         `json:"objects"`
	Views           []definitionmodel.ViewSchema                           `json:"views"`
	Actions         []definitionmodel.ActionSchema                         `json:"actions"`
	GuardedWrites   []appschemamodel.ApplicationSchemaGuardedWriteContract `json:"guarded_writes,omitempty"`
	Workflows       []definitionmodel.WorkflowSchema                       `json:"workflows"`
	Dictionaries    []appschemamodel.DictionarySchema                      `json:"dictionaries,omitempty"`
}

type PublishedSurfaceContextDTO struct {
	Surface               string   `json:"surface"`
	UserID                string   `json:"user_id"`
	RoleKey               string   `json:"role_key"`
	Permissions           []string `json:"permissions"`
	ActiveBusinessProfile string   `json:"active_business_profile,omitempty"`
	AuthorizationRevision string   `json:"authorization_revision,omitempty"`
	SchemaHash            string   `json:"schema_hash"`
}

type OpsMetadataDiagnosticsDTO struct {
	RepositoryRevision string `json:"repository_revision"`
	SchemaHash         string `json:"schema_hash"`
	SnapshotVersion    string `json:"snapshot_version"`
	TemplateVersion    string `json:"template_version"`
	ObjectCount        int    `json:"object_count"`
	ActionCount        int    `json:"action_count"`
	WorkflowCount      int    `json:"workflow_count"`
	Compatible         bool   `json:"compatible"`
}

func (s *ApplicationSchemaQueryApplicationService) PublishedRuntimeSchema(ctx context.Context, principal principalmodel.Principal) (PublishedRuntimeSchemaDTO, error) {
	if _, err := principalmodel.QueryScopeForPrincipal(principal); err != nil {
		return PublishedRuntimeSchemaDTO{}, &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.workspace_scope_required", Err: err}
	}
	snapshot := s.ForPrincipal(ctx, principal)
	objects := append([]definitionmodel.ObjectSchema(nil), snapshot.Objects...)
	for index := range objects {
		objects[index].Config = sanitizePublishedMap(objects[index].Config)
		objects[index].UX = sanitizePublishedMap(objects[index].UX)
	}
	views := append([]definitionmodel.ViewSchema(nil), snapshot.Views...)
	for index := range views {
		views[index].Config = sanitizePublishedMap(views[index].Config)
	}
	return PublishedRuntimeSchemaDTO{
		TemplateID: snapshot.TemplateID, TemplateVersion: snapshot.TemplateVersion, Name: snapshot.Name,
		SchemaHash: snapshot.SchemaHash, SnapshotVersion: snapshot.SnapshotVersion,
		Objects: objects, Views: views, Actions: append([]definitionmodel.ActionSchema(nil), snapshot.Actions...),
		GuardedWrites: append([]appschemamodel.ApplicationSchemaGuardedWriteContract(nil), snapshot.GuardedWrites...),
		Workflows:     append([]definitionmodel.WorkflowSchema(nil), snapshot.Workflows...),
		Dictionaries:  append([]appschemamodel.DictionarySchema(nil), snapshot.Dictionaries...),
	}, nil
}

func (s *ApplicationSchemaQueryApplicationService) PublishedSurfaceContext(ctx context.Context, surface string, principal principalmodel.Principal) (PublishedSurfaceContextDTO, error) {
	if _, err := principalmodel.QueryScopeForPrincipal(principal); err != nil {
		return PublishedSurfaceContextDTO{}, &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.workspace_scope_required", Err: err}
	}
	if strings.TrimSpace(principal.SurfaceKey) != "" && strings.TrimSpace(principal.SurfaceKey) != strings.TrimSpace(surface) {
		return PublishedSurfaceContextDTO{}, &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.surface.audience_required"}
	}
	snapshot := s.ForPrincipal(ctx, principal)
	profile := ""
	if principal.ActiveBusinessProfile != nil {
		profile = principal.ActiveBusinessProfile.BindingKey
	}
	return PublishedSurfaceContextDTO{
		Surface: strings.TrimSpace(surface), UserID: principal.UserID, RoleKey: principal.RoleKey,
		Permissions: principal.PermissionKeys(), ActiveBusinessProfile: profile,
		AuthorizationRevision: principal.AuthorizationRevision, SchemaHash: snapshot.SchemaHash,
	}, nil
}

func (s *ApplicationSchemaApplicationService) OpsMetadataDiagnostics(ctx context.Context, principal principalmodel.Principal) (OpsMetadataDiagnosticsDTO, error) {
	if _, err := principalmodel.QueryScopeForPrincipal(principal); err != nil {
		return OpsMetadataDiagnosticsDTO{}, &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.workspace_scope_required", Err: err}
	}
	if !metadataHasExactPermission(principal, PermissionMetadataOpsRead) {
		return OpsMetadataDiagnosticsDTO{}, forbidden("auth.permission_denied")
	}
	if s == nil || s.repository == nil || s.runtime == nil {
		return OpsMetadataDiagnosticsDTO{}, &apperror.AppError{Kind: apperror.KindUnavailable, Code: "backend.metadata.repository_unavailable"}
	}
	revision, err := s.repository.SnapshotRevision(ctx, metadataInstallationScope("inspect metadata repository revision"))
	if err != nil {
		return OpsMetadataDiagnosticsDTO{}, wrapMetadataError(err)
	}
	snapshot := s.runtime.Schema()
	return OpsMetadataDiagnosticsDTO{
		RepositoryRevision: revision, SchemaHash: snapshot.SchemaHash, SnapshotVersion: snapshot.SnapshotVersion,
		TemplateVersion: snapshot.TemplateVersion, ObjectCount: len(snapshot.Objects), ActionCount: len(snapshot.Actions),
		WorkflowCount: len(snapshot.Workflows), Compatible: strings.TrimSpace(revision) != "" && strings.TrimSpace(snapshot.SchemaHash) != "",
	}, nil
}

func metadataHasExactPermission(principal principalmodel.Principal, permission string) bool {
	if !principal.Known {
		return false
	}
	return principal.HasExactPermission(permission)
}

func sanitizePublishedMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		normalized := strings.ToLower(strings.TrimSpace(key))
		if strings.Contains(normalized, "secret") || strings.Contains(normalized, "password") ||
			strings.Contains(normalized, "token") || strings.Contains(normalized, "sql") ||
			strings.HasPrefix(normalized, "internal_") {
			continue
		}
		switch typed := value.(type) {
		case map[string]any:
			out[key] = sanitizePublishedMap(typed)
		case []any:
			values := make([]any, 0, len(typed))
			for _, item := range typed {
				if nested, ok := item.(map[string]any); ok {
					values = append(values, sanitizePublishedMap(nested))
				} else {
					values = append(values, item)
				}
			}
			out[key] = values
		default:
			out[key] = value
		}
	}
	return out
}
