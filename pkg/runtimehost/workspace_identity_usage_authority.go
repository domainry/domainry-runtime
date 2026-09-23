package runtimehost

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	auditcontract "github.com/domainry/domainry-audit-sdk/contract"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	identitymodulehost "github.com/domainry/domainry-identity-sdk/modulehost"
	"github.com/domainry/domainry-orm/query"
	workspaceprovisionvalidation "github.com/domainry/domainry-runtime/runtime/domain/workspaceprovision/validation"
	runtimeauditmodule "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/auditmodule"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/workspaceprovision"
)

const (
	workspaceIdentityUsageAdministratorRole = "tenant_admin"
	workspaceIdentityUsageAuditTimeout      = 2 * time.Second
)

type projectIdentityUsageOptions struct {
	ApplicationKey string
	CursorSecret   string
}

type runtimeWorkspaceIdentityUsageAuthority struct {
	mu             sync.RWMutex
	runtime        *database.RuntimeStore
	applicationKey string
	authenticator  identitysdk.PrincipalAuthenticator
}

func newRuntimeWorkspaceIdentityUsageAuthority(store *database.RuntimeStore, applicationKey string) *runtimeWorkspaceIdentityUsageAuthority {
	return &runtimeWorkspaceIdentityUsageAuthority{runtime: store, applicationKey: strings.TrimSpace(applicationKey)}
}

func (authority *runtimeWorkspaceIdentityUsageAuthority) BindAuthenticator(authenticator identitysdk.PrincipalAuthenticator) error {
	if authority == nil || authenticator == nil {
		return fmt.Errorf("Workspace identity usage authenticator is required")
	}
	authority.mu.Lock()
	defer authority.mu.Unlock()
	authority.authenticator = authenticator
	return nil
}

func (authority *runtimeWorkspaceIdentityUsageAuthority) AuthorizeWorkspaceIdentityUsage(ctx context.Context, request identitymodulehost.WorkspaceIdentityUsageAuthorizationRequest) (identitymodulehost.WorkspaceIdentityUsageGrant, error) {
	if authority == nil || authority.runtime == nil || authority.applicationKey == "" {
		return identitymodulehost.WorkspaceIdentityUsageGrant{}, workspaceIdentityUsageAuthorityError(http.StatusInternalServerError, "identity.workspace_usage_authority_unavailable", nil)
	}
	installation, found, err := workspaceprovision.LoadInstallation(ctx, authority.runtime)
	if err != nil || !found {
		return identitymodulehost.WorkspaceIdentityUsageGrant{}, workspaceIdentityUsageAuthorityError(http.StatusInternalServerError, "identity.workspace_usage_installation_unavailable", err)
	}
	permissionKey := strings.TrimSpace(request.PermissionKey)
	authority.mu.RLock()
	authenticator := authority.authenticator
	authority.mu.RUnlock()
	var principal identitysdk.Principal
	denialCode := ""
	if permissionKey != identitysdk.WorkspaceIdentityUsageAggregatePermission || strings.TrimSpace(request.AccessToken) == "" || authenticator == nil {
		denialCode = "identity.workspace_usage_authority_denied"
	} else {
		principal, err = authenticator.Authenticate(ctx, strings.TrimSpace(request.AccessToken))
		if err != nil {
			denialCode = "identity.workspace_usage_authentication_failed"
		} else if !principal.Known || strings.TrimSpace(principal.WorkspaceID) != strings.TrimSpace(installation.WorkspaceID) ||
			strings.TrimSpace(principal.RoleKey) != workspaceIdentityUsageAdministratorRole || strings.TrimSpace(principal.UserID) == "" || strings.TrimSpace(principal.AuthorizationRevision) == "" {
			denialCode = "identity.workspace_usage_authority_denied"
		}
	}
	outcome := "allowed"
	if denialCode != "" {
		outcome = "denied"
	}
	auditID, auditErr := authority.appendAuthorizationAudit(installation, principal, permissionKey, outcome, denialCode)
	if auditErr != nil {
		return identitymodulehost.WorkspaceIdentityUsageGrant{}, workspaceIdentityUsageAuthorityError(http.StatusInternalServerError, "identity.workspace_usage_authority_audit_failed", auditErr)
	}
	if denialCode != "" {
		return identitymodulehost.WorkspaceIdentityUsageGrant{}, workspaceIdentityUsageAuthorityError(http.StatusForbidden, denialCode, err)
	}
	installationID, err := authority.runtime.InstallationIdentity(ctx)
	if err != nil {
		return identitymodulehost.WorkspaceIdentityUsageGrant{}, workspaceIdentityUsageAuthorityError(http.StatusInternalServerError, "identity.workspace_usage_installation_unavailable", err)
	}
	return identitymodulehost.WorkspaceIdentityUsageGrant{
		InstallationID: installationID, ApplicationKey: authority.applicationKey, SubjectID: strings.TrimSpace(principal.UserID),
		AuditWorkspaceID: strings.TrimSpace(installation.WorkspaceID), PermissionKey: permissionKey,
		AuthorizationRevision: strings.TrimSpace(principal.AuthorizationRevision), AuthorizationAuditID: auditID,
	}, nil
}

func (authority *runtimeWorkspaceIdentityUsageAuthority) ListAuthorizedWorkspaceIdentityUsage(ctx context.Context, grant identitymodulehost.WorkspaceIdentityUsageGrant, request identitymodulehost.WorkspaceIdentityUsageCatalogQuery) (identitymodulehost.WorkspaceIdentityUsageCatalogPage, error) {
	if err := authority.validateGrant(ctx, grant); err != nil {
		return identitymodulehost.WorkspaceIdentityUsageCatalogPage{}, err
	}
	if request.Limit < 1 || request.Limit > identitysdk.WorkspaceIdentityUsageMaxPageSize+1 {
		return identitymodulehost.WorkspaceIdentityUsageCatalogPage{}, workspaceIdentityUsageAuthorityError(http.StatusBadRequest, "identity.workspace_usage_catalog_request_invalid", nil)
	}
	executor := database.ActionExecutionTransaction(ctx)
	if executor == nil {
		return identitymodulehost.WorkspaceIdentityUsageCatalogPage{}, workspaceIdentityUsageAuthorityError(http.StatusInternalServerError, "identity.workspace_usage_transaction_required", nil)
	}
	revision, err := authority.catalogRevision(ctx, executor, grant.InstallationID)
	if err != nil {
		return identitymodulehost.WorkspaceIdentityUsageCatalogPage{}, err
	}
	if expected := strings.TrimSpace(request.ExpectedCatalogRevision); expected != "" && expected != revision {
		return identitymodulehost.WorkspaceIdentityUsageCatalogPage{}, workspaceIdentityUsageAuthorityError(http.StatusConflict, "identity.workspace_usage_catalog_revision_conflict", nil)
	}
	after := strings.TrimSpace(request.AfterWorkspaceID)
	predicate := query.EqualExpressions(query.QualifiedColumn("workspace", "status"), query.Value("active"))
	if after != "" {
		predicate = query.And(predicate, query.GreaterThanExpressions(query.QualifiedColumn("workspace", "id"), query.Value(after)))
	}
	statement, arguments, err := query.NewSelectBuilder(authority.runtime.RuntimeRenderer(), "_workspaces").Alias("workspace").
		Columns("id", "status").Where(predicate).OrderBy(query.Ascending("id")).Limit(request.Limit).Build()
	if err != nil {
		return identitymodulehost.WorkspaceIdentityUsageCatalogPage{}, workspaceIdentityUsageAuthorityError(http.StatusInternalServerError, "identity.workspace_usage_catalog_unavailable", err)
	}
	rows, err := executor.QueryContext(ctx, statement, arguments...)
	if err != nil {
		return identitymodulehost.WorkspaceIdentityUsageCatalogPage{}, workspaceIdentityUsageAuthorityError(http.StatusInternalServerError, "identity.workspace_usage_catalog_unavailable", err)
	}
	defer rows.Close()
	page := identitymodulehost.WorkspaceIdentityUsageCatalogPage{CatalogRevision: revision}
	for rows.Next() {
		var workspaceID, status string
		if err := rows.Scan(&workspaceID, &status); err != nil {
			return identitymodulehost.WorkspaceIdentityUsageCatalogPage{}, err
		}
		page.Workspaces = append(page.Workspaces, identitymodulehost.WorkspaceIdentityUsageCatalogEntry{
			WorkspaceID: strings.TrimSpace(workspaceID), Status: identitymodulehost.WorkspaceIdentityUsageCatalogStatus(strings.TrimSpace(status)), Known: true, Authorized: true,
		})
	}
	if err := rows.Err(); err != nil {
		return identitymodulehost.WorkspaceIdentityUsageCatalogPage{}, err
	}
	return page, nil
}

func (authority *runtimeWorkspaceIdentityUsageAuthority) ResolveAuthorizedWorkspaceIdentityUsage(ctx context.Context, grant identitymodulehost.WorkspaceIdentityUsageGrant, request identitymodulehost.WorkspaceIdentityUsageCatalogResolve) (identitymodulehost.WorkspaceIdentityUsageCatalogEntry, error) {
	if err := authority.validateGrant(ctx, grant); err != nil {
		return identitymodulehost.WorkspaceIdentityUsageCatalogEntry{}, err
	}
	workspaceCode := workspaceprovisionvalidation.NormalizeCanonicalCode(request.WorkspaceCode)
	if workspaceCode == "" || workspaceCode != request.WorkspaceCode || workspaceprovisionvalidation.ValidateCanonicalCode(workspaceCode) != nil {
		return identitymodulehost.WorkspaceIdentityUsageCatalogEntry{}, workspaceIdentityUsageAuthorityError(http.StatusBadRequest, "identity.workspace_usage_catalog_request_invalid", nil)
	}
	executor := database.ActionExecutionTransaction(ctx)
	if executor == nil {
		return identitymodulehost.WorkspaceIdentityUsageCatalogEntry{}, workspaceIdentityUsageAuthorityError(http.StatusInternalServerError, "identity.workspace_usage_transaction_required", nil)
	}
	statement, arguments, err := query.NewSelectBuilder(authority.runtime.RuntimeRenderer(), "_workspaces").Columns("id", "status").
		Where(query.And(query.Equal("canonical_code", workspaceCode), query.Equal("status", "active"))).Limit(1).Build()
	if err != nil {
		return identitymodulehost.WorkspaceIdentityUsageCatalogEntry{}, workspaceIdentityUsageAuthorityError(http.StatusInternalServerError, "identity.workspace_usage_catalog_unavailable", err)
	}
	var resolvedID, status string
	if err := executor.QueryRowContext(ctx, statement, arguments...).Scan(&resolvedID, &status); errors.Is(err, sql.ErrNoRows) {
		return identitymodulehost.WorkspaceIdentityUsageCatalogEntry{}, workspaceIdentityUsageAuthorityError(http.StatusForbidden, "identity.workspace_usage_catalog_scope_denied", nil)
	} else if err != nil {
		return identitymodulehost.WorkspaceIdentityUsageCatalogEntry{}, workspaceIdentityUsageAuthorityError(http.StatusInternalServerError, "identity.workspace_usage_catalog_unavailable", err)
	}
	return identitymodulehost.WorkspaceIdentityUsageCatalogEntry{
		WorkspaceID: strings.TrimSpace(resolvedID), Status: identitymodulehost.WorkspaceIdentityUsageCatalogStatus(strings.TrimSpace(status)), Known: true, Authorized: true,
	}, nil
}

func (authority *runtimeWorkspaceIdentityUsageAuthority) validateGrant(ctx context.Context, grant identitymodulehost.WorkspaceIdentityUsageGrant) error {
	if authority == nil || authority.runtime == nil {
		return workspaceIdentityUsageAuthorityError(http.StatusInternalServerError, "identity.workspace_usage_authority_unavailable", nil)
	}
	installation, found, err := workspaceprovision.LoadInstallation(ctx, authority.runtime)
	if err != nil || !found {
		return workspaceIdentityUsageAuthorityError(http.StatusInternalServerError, "identity.workspace_usage_installation_unavailable", err)
	}
	installationID, err := authority.runtime.InstallationIdentity(ctx)
	if err != nil {
		return workspaceIdentityUsageAuthorityError(http.StatusInternalServerError, "identity.workspace_usage_installation_unavailable", err)
	}
	if strings.TrimSpace(grant.InstallationID) != installationID || strings.TrimSpace(grant.ApplicationKey) != authority.applicationKey ||
		strings.TrimSpace(grant.AuditWorkspaceID) != strings.TrimSpace(installation.WorkspaceID) || strings.TrimSpace(grant.PermissionKey) != identitysdk.WorkspaceIdentityUsageAggregatePermission ||
		strings.TrimSpace(grant.SubjectID) == "" || strings.TrimSpace(grant.AuthorizationRevision) == "" || strings.TrimSpace(grant.AuthorizationAuditID) == "" {
		return workspaceIdentityUsageAuthorityError(http.StatusForbidden, "identity.workspace_usage_grant_invalid", nil)
	}
	statement, arguments, err := query.NewWorkspaceSelectBuilder(authority.runtime.RuntimeRenderer(), "_audit_events", installation.WorkspaceID).
		Columns("event", "actor_id", "role_key", "metadata_json").Where(query.Equal("id", strings.TrimSpace(grant.AuthorizationAuditID))).Limit(1).Build()
	if err != nil {
		return workspaceIdentityUsageAuthorityError(http.StatusInternalServerError, "identity.workspace_usage_authority_audit_unavailable", err)
	}
	var event, actorID, roleKey, metadataJSON string
	if err := authority.runtime.DB().QueryRowContext(ctx, statement, arguments...).Scan(&event, &actorID, &roleKey, &metadataJSON); err != nil {
		return workspaceIdentityUsageAuthorityError(http.StatusForbidden, "identity.workspace_usage_authority_audit_invalid", err)
	}
	var metadata map[string]any
	if json.Unmarshal([]byte(metadataJSON), &metadata) != nil || event != "identity.workspace_identity_usage.authorize" || strings.TrimSpace(actorID) != strings.TrimSpace(grant.SubjectID) ||
		strings.TrimSpace(roleKey) != workspaceIdentityUsageAdministratorRole || metadata["outcome"] != "allowed" || metadata["application_key"] != authority.applicationKey ||
		metadata["principal_snapshot"] != workspaceIdentityUsageAuthorizationRevisionDigest(grant.AuthorizationRevision) {
		return workspaceIdentityUsageAuthorityError(http.StatusForbidden, "identity.workspace_usage_authority_audit_invalid", nil)
	}
	return nil
}

func (authority *runtimeWorkspaceIdentityUsageAuthority) catalogRevision(ctx context.Context, executor database.ActionExecutionExecutor, installationID string) (string, error) {
	statement, arguments, err := query.NewSelectBuilder(authority.runtime.RuntimeRenderer(), "_workspaces").Alias("workspace").
		Projections(
			query.Project(query.QualifiedColumn("workspace", "id")), query.Project(query.QualifiedColumn("workspace", "revision")),
			query.Project(query.QualifiedColumn("workspace", "commercial_revision")),
		).
		Where(query.EqualExpressions(query.QualifiedColumn("workspace", "status"), query.Value("active"))).OrderBy(query.AscendingExpression(query.QualifiedColumn("workspace", "id"))).Build()
	if err != nil {
		return "", workspaceIdentityUsageAuthorityError(http.StatusInternalServerError, "identity.workspace_usage_catalog_unavailable", err)
	}
	rows, err := executor.QueryContext(ctx, statement, arguments...)
	if err != nil {
		return "", workspaceIdentityUsageAuthorityError(http.StatusInternalServerError, "identity.workspace_usage_catalog_unavailable", err)
	}
	defer rows.Close()
	hash := sha256.New()
	_, _ = hash.Write([]byte("domainry.runtime.workspace-identity-usage.catalog.v1\x00" + strings.TrimSpace(installationID)))
	for rows.Next() {
		var workspaceID string
		var workspaceRevision, commercialRevision int64
		if err := rows.Scan(&workspaceID, &workspaceRevision, &commercialRevision); err != nil {
			return "", err
		}
		_, _ = hash.Write([]byte("\x00" + strings.TrimSpace(workspaceID) + "\x00" + fmt.Sprint(workspaceRevision) + "\x00" + fmt.Sprint(commercialRevision)))
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func (authority *runtimeWorkspaceIdentityUsageAuthority) appendAuthorizationAudit(installation workspaceprovision.Installation, principal identitysdk.Principal, permissionKey, outcome, errorCode string) (string, error) {
	auditCtx, cancel := context.WithTimeout(context.Background(), workspaceIdentityUsageAuditTimeout)
	defer cancel()
	event, err := auditcontract.BuildEvent(auditcontract.AppendRequest{
		Family: auditcontract.EventFamilyIdentityGovernance, Event: "identity.workspace_identity_usage.authorize", ObjectKey: "identity.workspace_identity_usage", RecordID: "installation",
		Actor: auditcontract.Actor{
			WorkspaceID: strings.TrimSpace(installation.WorkspaceID), SubjectID: strings.TrimSpace(principal.UserID), RoleKey: strings.TrimSpace(principal.RoleKey),
			Kind: "user", AuthorizationRevision: strings.TrimSpace(principal.AuthorizationRevision),
		},
		Summary: "Authorized Workspace identity usage aggregate",
		Metadata: map[string]any{
			"application_key": authority.applicationKey, "permission_key": permissionKey, "outcome": outcome, "error_code": errorCode,
			"principal_snapshot": workspaceIdentityUsageAuthorizationRevisionDigest(principal.AuthorizationRevision),
		},
	}, time.Now())
	if err != nil {
		return "", err
	}
	if err := runtimeauditmodule.NewAuditStoreFromRuntimeStore(auditCtx, authority.runtime).InsertAuditEvent(auditCtx, installation.WorkspaceID, event); err != nil {
		return "", err
	}
	return event.ID, nil
}

func workspaceIdentityUsageAuthorizationRevisionDigest(revision string) string {
	digest := sha256.Sum256([]byte("domainry.runtime.workspace-identity-usage.authorization.v1\x00" + strings.TrimSpace(revision)))
	return hex.EncodeToString(digest[:])
}

func workspaceIdentityUsageCursorKey(secret string) []byte {
	digest := sha256.Sum256([]byte("domainry.runtime.workspace-identity-usage.cursor.v1\x00" + strings.TrimSpace(secret)))
	return append([]byte(nil), digest[:]...)
}

func workspaceIdentityUsageAuthorityError(status int, code string, cause error) error {
	return &identitysdk.Error{StatusCode: status, Code: code, Cause: cause}
}

var _ identitymodulehost.WorkspaceIdentityUsageInstallationAuthority = (*runtimeWorkspaceIdentityUsageAuthority)(nil)
