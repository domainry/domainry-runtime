// Package knowledgehost adapts Runtime-owned Identity and record policy ports
// to the provider-neutral Knowledge module contracts.
package knowledgehost

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/requestcontext"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-identity-sdk/authorization/evaluator"
	knowledgesdk "github.com/domainry/domainry-knowledge-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

type Records interface {
	GetRecord(context.Context, string, string, principalmodel.Principal) (recordmodel.Record, error)
}

type Host struct {
	runtimeID   string
	application identitysdk.ApplicationScope
	principals  identitysdk.PrincipalResolver
	projection  identitysdk.Projection
	records     Records
}

func New(runtimeID string, application identitysdk.ApplicationScope, principals identitysdk.PrincipalResolver, projection identitysdk.Projection, records Records) (*Host, error) {
	if len(strings.TrimSpace(runtimeID)) == 0 || len(strings.TrimSpace(string(application.WorkspaceID))) == 0 || len(strings.TrimSpace(string(application.ApplicationKey))) == 0 || principals == nil || projection == nil || records == nil {
		return nil, &knowledgesdk.Error{Class: "unavailable", Code: "knowledge.host_incomplete"}
	}
	return &Host{runtimeID: strings.TrimSpace(runtimeID), application: application, principals: principals, projection: projection, records: records}, nil
}

func (h *Host) resolve(ctx context.Context, authority knowledgesdk.Authority) (principalmodel.Principal, error) {
	if !authority.Known || authority.RuntimeID != h.runtimeID || len(strings.TrimSpace(authority.WorkspaceID)) == 0 || len(strings.TrimSpace(authority.UserID)) == 0 {
		return principalmodel.Principal{}, denied("knowledge.library_access_denied")
	}
	// The authenticated principal owns the request Workspace. External Identity
	// providers may resolve one application installation to multiple tenant
	// Workspaces, so the installation Workspace is not an authorization bound.
	// Resolve in the selected Workspace and verify the returned principal below.
	resolution, err := h.principals.Resolve(requestcontext.WithWorkspaceID(ctx, authority.WorkspaceID), identitysdk.PrincipalResolutionRequest{SubjectID: identitysdk.SubjectID(authority.UserID), SessionRoleKey: authority.RoleKey})
	if err != nil {
		return principalmodel.Principal{}, classify("knowledge.identity_unavailable", err)
	}
	principal := resolution.Principal
	if !principal.Known || principal.UserID != authority.UserID || principal.WorkspaceID != authority.WorkspaceID {
		return principalmodel.Principal{}, denied("knowledge.library_access_denied")
	}
	principal.AccessBundle = &resolution.AccessBundle
	return principalmodel.NewPrincipalFromIdentity(principal, ""), nil
}

func (h *Host) AuthorizeKnowledgeLibrary(ctx context.Context, operation string, library knowledgesdk.KnowledgeLibrary, authority knowledgesdk.Authority) error {
	action, found, err := knowledgesdk.AuthorizationAction(operation)
	if err != nil {
		return err
	}
	if !found || action.Permission == nil {
		return denied("knowledge.library_access_denied")
	}
	principal, err := h.resolve(ctx, authority)
	if err != nil {
		return err
	}
	// A concrete library is authorized by Knowledge's live membership and page
	// policy. Identity is deliberately evaluated only for global capabilities,
	// otherwise every reader/editor/manager grant would have to be configured a
	// second time in the product role.
	if !knowledgesdk.RequiresIdentityActionGrant(operation, library) {
		return nil
	}
	facts := identitysdk.ResourceFacts{
		"workspace_id": authority.WorkspaceID, "library_id": library.ID, "library_kind": library.Kind,
		"library_role": library.Role, "owner_user_id": library.OwnerUserID,
	}
	decision, err := evaluator.Evaluate(*principal.AccessBundle, identitysdk.AccessRequest{ObjectKey: action.Permission.ResourceKey, Action: action.Permission.OperationKey}, facts, time.Now().UTC())
	if err != nil {
		return classify("knowledge.authorization_unavailable", err)
	}
	if !decision.Allowed {
		return denied("knowledge.library_access_denied")
	}
	return nil
}

func (h *Host) ValidateKnowledgeLibraryMember(ctx context.Context, userID string, authority knowledgesdk.Authority) error {
	if strings.TrimSpace(userID) == "" || len(userID) > 255 {
		return &knowledgesdk.Error{Class: "bad_request", Code: "knowledge.library_member_unavailable"}
	}
	if _, err := h.resolve(ctx, authority); err != nil {
		return err
	}
	resolution, err := h.principals.Resolve(requestcontext.WithWorkspaceID(ctx, authority.WorkspaceID), identitysdk.PrincipalResolutionRequest{SubjectID: identitysdk.SubjectID(userID)})
	if err != nil || !resolution.Principal.Known || resolution.Principal.UserID != userID || resolution.Principal.WorkspaceID != authority.WorkspaceID {
		return &knowledgesdk.Error{Class: "bad_request", Code: "knowledge.library_member_unavailable"}
	}
	return nil
}

func (h *Host) ValidateKnowledgeLibraryOrganizationMember(ctx context.Context, organizationID string, authority knowledgesdk.Authority) error {
	organizationID = strings.TrimSpace(organizationID)
	if organizationID == "" || len(organizationID) > 255 {
		return &knowledgesdk.Error{Class: "bad_request", Code: "knowledge.library_organization_member_unavailable"}
	}
	if _, err := h.resolve(ctx, authority); err != nil {
		return err
	}
	organization, found, err := h.projection.FindOrganizationUnit(requestcontext.WithWorkspaceID(ctx, authority.WorkspaceID), identitysdk.OrganizationUnitLookup{OrgID: organizationID})
	if err != nil || !found || organization.OrganizationUnitID != organizationID || !strings.EqualFold(strings.TrimSpace(organization.Status), "active") {
		return &knowledgesdk.Error{Class: "bad_request", Code: "knowledge.library_organization_member_unavailable"}
	}
	return nil
}

func (h *Host) AuthorizeKnowledgeDocumentSource(ctx context.Context, source knowledgesdk.KnowledgeDocumentSourceAccess, authority knowledgesdk.Authority) error {
	if source.Namespace != knowledgesdk.KnowledgeDocumentSourceNamespaceRuntimeRecord || strings.TrimSpace(source.ResourceType) == "" || strings.TrimSpace(source.ResourceID) == "" {
		return denied("knowledge.document_source_access_denied")
	}
	principal, err := h.resolve(ctx, authority)
	if err != nil {
		return err
	}
	record, err := h.records.GetRecord(ctx, source.ResourceType, source.ResourceID, principal)
	if err != nil {
		return classify("knowledge.document_source_unavailable", err)
	}
	if record.RecordID != source.ResourceID {
		return denied("knowledge.document_source_access_denied")
	}
	return nil
}

func (h *Host) SearchKnowledgePrincipals(ctx context.Context, query, after string, limit int, authority knowledgesdk.Authority) (knowledgesdk.KnowledgePrincipalPage, error) {
	if _, err := h.resolve(ctx, authority); err != nil {
		return knowledgesdk.KnowledgePrincipalPage{}, err
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		return knowledgesdk.KnowledgePrincipalPage{}, &knowledgesdk.Error{Class: "bad_request", Code: "knowledge.principal_limit_invalid"}
	}
	users, err := h.projection.ListUsers(requestcontext.WithWorkspaceID(ctx, authority.WorkspaceID), identitysdk.ProjectionQuery{})
	if err != nil {
		return knowledgesdk.KnowledgePrincipalPage{}, classify("knowledge.principal_directory_unavailable", err)
	}
	needle := strings.ToLower(strings.TrimSpace(query))
	items := make([]knowledgesdk.KnowledgePrincipal, 0, len(users))
	for _, user := range users {
		if user.Status != identitysdk.UserStatusActive || user.UserID <= after {
			continue
		}
		if needle != "" && !strings.Contains(strings.ToLower(user.UserID+"\n"+user.Name+"\n"+user.Email), needle) {
			continue
		}
		items = append(items, knowledgesdk.KnowledgePrincipal{UserID: user.UserID, DisplayName: user.Name, Email: user.Email, Status: user.Status})
	}
	sort.Slice(items, func(left, right int) bool { return items[left].UserID < items[right].UserID })
	result := knowledgesdk.KnowledgePrincipalPage{Items: items, Complete: true}
	if len(result.Items) > limit {
		result.Items = result.Items[:limit]
		result.NextAfter = result.Items[len(result.Items)-1].UserID
		result.Complete = false
	}
	return result, nil
}

func (h *Host) ResolveKnowledgePrincipals(ctx context.Context, userIDs []string, authority knowledgesdk.Authority) ([]knowledgesdk.KnowledgePrincipal, error) {
	if len(userIDs) > 100 {
		return nil, &knowledgesdk.Error{Class: "bad_request", Code: "knowledge.principal_limit_invalid"}
	}
	if _, err := h.resolve(ctx, authority); err != nil {
		return nil, err
	}
	result := make([]knowledgesdk.KnowledgePrincipal, 0, len(userIDs))
	for _, userID := range userIDs {
		user, found, err := h.projection.FindUser(requestcontext.WithWorkspaceID(ctx, authority.WorkspaceID), identitysdk.UserLookup{UserID: identitysdk.SubjectID(userID)})
		if err != nil {
			return nil, classify("knowledge.principal_directory_unavailable", err)
		}
		if !found || user.Status != identitysdk.UserStatusActive {
			result = append(result, knowledgesdk.KnowledgePrincipal{UserID: userID, DisplayName: "账号不可用", Status: "unavailable"})
			continue
		}
		result = append(result, knowledgesdk.KnowledgePrincipal{UserID: user.UserID, DisplayName: user.Name, Email: user.Email, Status: user.Status})
	}
	return result, nil
}

func (h *Host) ResolveKnowledgeOrganizationPrincipals(ctx context.Context, organizationIDs []string, authority knowledgesdk.Authority) ([]knowledgesdk.KnowledgeOrganizationPrincipal, error) {
	if len(organizationIDs) > 100 {
		return nil, &knowledgesdk.Error{Class: "bad_request", Code: "knowledge.principal_limit_invalid"}
	}
	if _, err := h.resolve(ctx, authority); err != nil {
		return nil, err
	}
	result := make([]knowledgesdk.KnowledgeOrganizationPrincipal, 0, len(organizationIDs))
	for _, organizationID := range organizationIDs {
		organizationID = strings.TrimSpace(organizationID)
		organization, found, err := h.projection.FindOrganizationUnit(requestcontext.WithWorkspaceID(ctx, authority.WorkspaceID), identitysdk.OrganizationUnitLookup{OrgID: organizationID})
		if err != nil {
			return nil, classify("knowledge.principal_directory_unavailable", err)
		}
		if !found || !strings.EqualFold(strings.TrimSpace(organization.Status), "active") {
			result = append(result, knowledgesdk.KnowledgeOrganizationPrincipal{OrganizationID: organizationID, DisplayName: "组织不可用", Status: "unavailable"})
			continue
		}
		result = append(result, knowledgesdk.KnowledgeOrganizationPrincipal{OrganizationID: organization.OrganizationUnitID, DisplayName: organization.Name, NodeType: organization.NodeType, Status: organization.Status})
	}
	return result, nil
}

func (h *Host) KnowledgePrincipalDirectorySnapshot(ctx context.Context, authority knowledgesdk.Authority) (knowledgesdk.KnowledgePrincipalDirectorySnapshot, error) {
	if _, err := h.resolve(ctx, authority); err != nil {
		return knowledgesdk.KnowledgePrincipalDirectorySnapshot{}, err
	}
	workspaceContext := requestcontext.WithWorkspaceID(ctx, authority.WorkspaceID)
	users, err := h.projection.ListUsers(workspaceContext, identitysdk.ProjectionQuery{})
	if err != nil {
		return knowledgesdk.KnowledgePrincipalDirectorySnapshot{}, classify("knowledge.principal_directory_unavailable", err)
	}
	if len(users) > 5000 {
		return knowledgesdk.KnowledgePrincipalDirectorySnapshot{}, &knowledgesdk.Error{Class: knowledgesdk.ErrorClassUnavailable, Code: "knowledge.principal_directory_limit_exceeded"}
	}
	result := knowledgesdk.KnowledgePrincipalDirectorySnapshot{
		CurrentUserID: authority.UserID,
		Organizations: []knowledgesdk.KnowledgeDirectoryOrganization{},
		Members:       []knowledgesdk.KnowledgeDirectoryMember{},
		Complete:      true,
	}
	organizationQueue := []string{}
	queued := map[string]bool{}
	for _, user := range users {
		if user.Status != identitysdk.UserStatusActive {
			continue
		}
		organizationID := strings.TrimSpace(user.OrgID)
		result.Members = append(result.Members, knowledgesdk.KnowledgeDirectoryMember{
			KnowledgePrincipal: knowledgesdk.KnowledgePrincipal{UserID: user.UserID, DisplayName: user.Name, Email: user.Email, Status: user.Status},
			OrganizationID:     organizationID,
		})
		if organizationID != "" && !queued[organizationID] {
			queued[organizationID] = true
			organizationQueue = append(organizationQueue, organizationID)
		}
	}
	organizations := map[string]identitysdk.OrganizationUnit{}
	for len(organizationQueue) > 0 {
		organizationID := organizationQueue[0]
		organizationQueue = organizationQueue[1:]
		organization, found, lookupErr := h.projection.FindOrganizationUnit(workspaceContext, identitysdk.OrganizationUnitLookup{OrgID: organizationID})
		if lookupErr != nil {
			return knowledgesdk.KnowledgePrincipalDirectorySnapshot{}, classify("knowledge.principal_directory_unavailable", lookupErr)
		}
		if !found || !strings.EqualFold(strings.TrimSpace(organization.Status), "active") {
			continue
		}
		organizations[organization.OrganizationUnitID] = organization
		if len(organizations) > 2000 {
			return knowledgesdk.KnowledgePrincipalDirectorySnapshot{}, &knowledgesdk.Error{Class: knowledgesdk.ErrorClassUnavailable, Code: "knowledge.principal_directory_limit_exceeded"}
		}
		for _, ancestorID := range organization.AncestorIDs {
			ancestorID = strings.TrimSpace(ancestorID)
			if ancestorID != "" && !queued[ancestorID] {
				queued[ancestorID] = true
				organizationQueue = append(organizationQueue, ancestorID)
			}
		}
		if organization.ParentID != nil {
			parentID := strings.TrimSpace(*organization.ParentID)
			if parentID != "" && !queued[parentID] {
				queued[parentID] = true
				organizationQueue = append(organizationQueue, parentID)
			}
		}
	}
	for _, organization := range organizations {
		parentID := ""
		if organization.ParentID != nil {
			parentID = strings.TrimSpace(*organization.ParentID)
		}
		result.Organizations = append(result.Organizations, knowledgesdk.KnowledgeDirectoryOrganization{
			OrganizationID: organization.OrganizationUnitID,
			ParentID:       parentID,
			DisplayName:    organization.Name,
			NodeType:       organization.NodeType,
			Depth:          organization.Depth,
			SortOrder:      organization.SortOrder,
			Status:         organization.Status,
		})
	}
	sort.Slice(result.Organizations, func(left, right int) bool {
		if result.Organizations[left].Depth != result.Organizations[right].Depth {
			return result.Organizations[left].Depth < result.Organizations[right].Depth
		}
		if result.Organizations[left].SortOrder != result.Organizations[right].SortOrder {
			return result.Organizations[left].SortOrder < result.Organizations[right].SortOrder
		}
		if result.Organizations[left].DisplayName != result.Organizations[right].DisplayName {
			return result.Organizations[left].DisplayName < result.Organizations[right].DisplayName
		}
		return result.Organizations[left].OrganizationID < result.Organizations[right].OrganizationID
	})
	sort.Slice(result.Members, func(left, right int) bool {
		if result.Members[left].DisplayName != result.Members[right].DisplayName {
			return result.Members[left].DisplayName < result.Members[right].DisplayName
		}
		return result.Members[left].UserID < result.Members[right].UserID
	})
	return result, nil
}

func (h *Host) ResolveKnowledgeAccessAuthority(ctx context.Context, userID string, authority knowledgesdk.Authority) (knowledgesdk.Authority, error) {
	userID = strings.TrimSpace(userID)
	if !authority.Known || authority.RuntimeID != h.runtimeID || authority.WorkspaceID != string(h.application.WorkspaceID) || userID == "" || len(userID) > 255 {
		return knowledgesdk.Authority{}, &knowledgesdk.Error{Class: "bad_request", Code: "knowledge.access_explain_invalid"}
	}
	resolution, err := h.principals.Resolve(requestcontext.WithWorkspaceID(ctx, authority.WorkspaceID), identitysdk.PrincipalResolutionRequest{SubjectID: identitysdk.SubjectID(userID)})
	if err != nil {
		return knowledgesdk.Authority{}, classify("knowledge.identity_unavailable", err)
	}
	principal := resolution.Principal
	if !principal.Known || principal.WorkspaceID != authority.WorkspaceID || principal.UserID != userID {
		return knowledgesdk.Authority{}, &knowledgesdk.Error{Class: "not_found", Code: "knowledge.access_principal_unavailable"}
	}
	return knowledgesdk.Authority{Known: true, RuntimeID: authority.RuntimeID, WorkspaceID: principal.WorkspaceID, UserID: principal.UserID, RoleKey: principal.RoleKey, OrgID: principal.OrgID, OrgScopeIDs: append([]string(nil), principal.OrgScopeIDs...), AccountType: principal.User.AccountType}, nil
}

func (h *Host) ExplainKnowledgeAccess(ctx context.Context, input knowledgesdk.KnowledgeAccessExplainRequest, library knowledgesdk.KnowledgeLibrary, authority knowledgesdk.Authority) (knowledgesdk.KnowledgeAccessExplanation, error) {
	result := knowledgesdk.KnowledgeAccessExplanation{UserID: input.UserID, LibraryID: library.ID, PageID: input.PageID, Action: input.Action}
	action, found, err := knowledgesdk.AuthorizationAction(input.Action)
	if err != nil {
		return result, err
	}
	if !found || action.Permission == nil || strings.TrimSpace(input.UserID) == "" {
		return result, &knowledgesdk.Error{Class: "bad_request", Code: "knowledge.access_explain_invalid"}
	}
	if _, err := h.resolve(ctx, authority); err != nil {
		return result, err
	}
	resolution, err := h.principals.Resolve(requestcontext.WithWorkspaceID(ctx, authority.WorkspaceID), identitysdk.PrincipalResolutionRequest{SubjectID: identitysdk.SubjectID(input.UserID)})
	if err != nil {
		return result, classify("knowledge.identity_unavailable", err)
	}
	if !resolution.Principal.Known || resolution.Principal.WorkspaceID != authority.WorkspaceID {
		result.Identity = knowledgesdk.KnowledgeAccessLayer{Allowed: false, Code: "identity_principal_unavailable", Label: "Identity 功能权限"}
		return result, nil
	}
	facts := identitysdk.ResourceFacts{
		"workspace_id": authority.WorkspaceID, "library_id": library.ID, "library_kind": library.Kind,
		"library_role": library.Role, "owner_user_id": library.OwnerUserID,
	}
	decision, err := evaluator.Evaluate(resolution.AccessBundle, identitysdk.AccessRequest{ObjectKey: action.Permission.ResourceKey, Action: action.Permission.OperationKey}, facts, time.Now().UTC())
	if err != nil {
		return result, classify("knowledge.authorization_unavailable", err)
	}
	result.Identity = knowledgesdk.KnowledgeAccessLayer{Allowed: decision.Allowed, Code: decision.Code, Label: "Identity 功能权限", Source: decision.PolicyKey}
	return result, nil
}

func denied(code string) error {
	return &knowledgesdk.Error{Class: knowledgesdk.ErrorClassForbidden, Code: code}
}

func classify(code string, err error) error {
	class := knowledgesdk.ErrorClassUnavailable
	switch apperror.KindOf(err) {
	case apperror.KindBadRequest:
		class = knowledgesdk.ErrorClassBadRequest
	case apperror.KindForbidden, apperror.KindNotFound:
		class = knowledgesdk.ErrorClassForbidden
	case apperror.KindConflict:
		class = knowledgesdk.ErrorClassConflict
	}
	return &knowledgesdk.Error{Class: class, Code: code}
}

var _ knowledgesdk.KnowledgeLibraryAuthorizer = (*Host)(nil)
var _ knowledgesdk.KnowledgeDocumentSourceAuthorizer = (*Host)(nil)
var _ knowledgesdk.KnowledgePrincipalDirectory = (*Host)(nil)
var _ knowledgesdk.KnowledgePrincipalDirectorySnapshotProvider = (*Host)(nil)
var _ knowledgesdk.KnowledgeAccessExplainer = (*Host)(nil)
