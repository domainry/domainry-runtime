package transport

import (
	"context"
	"sort"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/requestcontext"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeengine"
	actionapplication "github.com/domainry/domainry-runtime/runtime/application/action"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	recordmutation "github.com/domainry/domainry-runtime/runtime/application/recordmutation"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

type projectEngine struct {
	records       *recordapplication.RecordApplicationService
	actions       *actionapplication.ActionApplicationService
	accountWrites integrationsdk.ConnectionAccountWrites
	identityUsers identitysdk.Projection
	principal     func(context.Context) (principalmodel.Principal, bool)
}

func newProjectEngine(
	records *recordapplication.RecordApplicationService,
	actions *actionapplication.ActionApplicationService,
	accountWrites integrationsdk.ConnectionAccountWrites,
	identityUsers identitysdk.Projection,
	principal func(context.Context) (principalmodel.Principal, bool),
) runtimeengine.Engine {
	return &projectEngine{records: records, actions: actions, accountWrites: accountWrites, identityUsers: identityUsers, principal: principal}
}

func (e *projectEngine) ValidateTaskAssignee(ctx context.Context, userID string) (runtimeengine.TaskAssignee, error) {
	principal, err := e.requestPrincipal(ctx)
	if err != nil {
		return runtimeengine.TaskAssignee{}, err
	}
	userID = strings.TrimSpace(userID)
	if userID == "" || !taskAssigneeAllowed(principal, userID) {
		return runtimeengine.TaskAssignee{}, runtimeengine.NewError(runtimeengine.ErrorForbidden, "backend.task.assignee_out_of_scope", nil, nil)
	}
	if e.identityUsers == nil {
		return runtimeengine.TaskAssignee{}, runtimeengine.NewError(runtimeengine.ErrorUnavailable, "backend.task.assignee_directory_unavailable", nil, nil)
	}
	user, found, err := e.identityUsers.FindUser(requestcontext.WithWorkspaceID(ctx, principal.WorkspaceID), identitysdk.UserLookup{UserID: identitysdk.SubjectID(userID)})
	if err != nil {
		return runtimeengine.TaskAssignee{}, runtimeengine.NewError(runtimeengine.ErrorUnavailable, "backend.task.assignee_lookup_failed", nil, err)
	}
	if !found || user.Status != identitysdk.UserStatusActive || user.AccountType != "human" {
		return runtimeengine.TaskAssignee{}, runtimeengine.NewError(runtimeengine.ErrorBadRequest, "backend.task.assignee_inactive", nil, nil)
	}
	return runtimeengine.TaskAssignee{ID: user.ID, Name: user.Name}, nil
}

func (e *projectEngine) ResolveTaskAssigneeNames(ctx context.Context, userIDs []string) (map[string]string, error) {
	principal, err := e.requestPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	resolver, ok := e.identityUsers.(identitysdk.DisplayNameProjection)
	if !ok {
		return nil, runtimeengine.NewError(runtimeengine.ErrorUnavailable, "backend.task.assignee_names_unavailable", nil, nil)
	}
	result, err := resolver.ResolveDisplayNames(requestcontext.WithWorkspaceID(ctx, principal.WorkspaceID), identitysdk.DisplayNameQuery{UserIDs: userIDs})
	if err != nil {
		return nil, runtimeengine.NewError(runtimeengine.ErrorUnavailable, "backend.task.assignee_names_failed", nil, err)
	}
	names := make(map[string]string, len(result.Users))
	for _, user := range result.Users {
		names[user.ID] = user.Name
	}
	return names, nil
}

func (e *projectEngine) ListTaskAssignees(ctx context.Context, query runtimeengine.TaskAssigneeQuery) (runtimeengine.TaskAssigneePage, error) {
	principal, err := e.requestPrincipal(ctx)
	if err != nil {
		return runtimeengine.TaskAssigneePage{}, err
	}
	if e.identityUsers == nil {
		return runtimeengine.TaskAssigneePage{}, runtimeengine.NewError(runtimeengine.ErrorUnavailable, "backend.task.assignee_directory_unavailable", nil, nil)
	}
	users, err := e.identityUsers.ListUsers(requestcontext.WithWorkspaceID(ctx, principal.WorkspaceID), identitysdk.ProjectionQuery{})
	if err != nil {
		return runtimeengine.TaskAssigneePage{}, runtimeengine.NewError(runtimeengine.ErrorUnavailable, "backend.task.assignee_lookup_failed", nil, err)
	}
	search := strings.ToLower(strings.TrimSpace(query.Search))
	items := make([]runtimeengine.TaskAssignee, 0)
	for _, user := range users {
		if user.Status != identitysdk.UserStatusActive || user.AccountType != "human" || !taskAssigneeAllowed(principal, user.ID) {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(user.Name), search) && !strings.Contains(strings.ToLower(user.Email), search) {
			continue
		}
		items = append(items, runtimeengine.TaskAssignee{ID: user.ID, Name: user.Name})
	}
	sort.Slice(items, func(left, right int) bool { return items[left].ID < items[right].ID })
	start := sort.Search(len(items), func(index int) bool { return items[index].ID > strings.TrimSpace(query.AfterID) })
	limit := query.Limit
	if limit <= 0 || limit > 50 {
		limit = 25
	}
	end := start + limit
	if end > len(items) {
		end = len(items)
	}
	page := runtimeengine.TaskAssigneePage{Items: items[start:end], Total: len(items)}
	if end < len(items) {
		page.NextAfterID = items[end-1].ID
	}
	return page, nil
}

func taskAssigneeAllowed(principal principalmodel.Principal, userID string) bool {
	if userID == principal.UserID {
		return true
	}
	if principal.RoleKey == "workspace_admin" {
		return true
	}
	if principal.RoleKey != "sales_manager" {
		return false
	}
	for _, reportingID := range principal.ReportingScopeUserIDs {
		if reportingID == userID {
			return true
		}
	}
	return false
}

func (e *projectEngine) AuthorizeConnectionAccountWrite(ctx context.Context, connectionKey, operationKey, contractSHA256 string) (runtimeengine.ConnectionAccountWriteAccess, error) {
	principal, err := e.requestPrincipal(ctx)
	if err != nil {
		return runtimeengine.ConnectionAccountWriteAccess{}, err
	}
	if e.accountWrites == nil {
		return runtimeengine.ConnectionAccountWriteAccess{}, runtimeengine.NewError(runtimeengine.ErrorUnavailable, "backend.connector.account_write_unavailable", nil, nil)
	}
	access, err := e.accountWrites.AuthorizeConnectionAccountWrite(ctx, integrationsdk.ConnectionAccountSubject{
		WorkspaceID: principal.WorkspaceID, UserID: principal.UserID,
		Access: integrationsdk.ConnectionAccountAccess{Personal: true},
	}, strings.TrimSpace(connectionKey), integrationsdk.ConnectionAccountWriteOperation{
		Operation: strings.TrimSpace(operationKey), ContractSHA256: strings.TrimSpace(contractSHA256),
	})
	if err != nil {
		return runtimeengine.ConnectionAccountWriteAccess{}, runtimeengine.NewError(runtimeengine.ErrorForbidden, "backend.connector.account_write_denied", nil, err)
	}
	return runtimeengine.ConnectionAccountWriteAccess{ConnectionKey: access.Source.ConnectionKey, ProviderKey: access.Source.ProviderKey}, nil
}

func (e *projectEngine) List(ctx context.Context, objectKey string, query runtimeengine.Query) (runtimeengine.Page, error) {
	principal, err := e.requestPrincipal(ctx)
	if err != nil {
		return runtimeengine.Page{}, err
	}
	page := query.Page
	if page <= 0 {
		page = 1
	}
	pageSize := query.PageSize
	if pageSize <= 0 {
		pageSize = 50
	}
	sorts := make([]recordmodel.RecordSortRule, 0, len(query.Sorts))
	for _, sort := range query.Sorts {
		sorts = append(sorts, recordmodel.RecordSortRule{Field: strings.TrimSpace(sort.Field), Direction: strings.TrimSpace(sort.Direction)})
	}
	result, serviceErr := e.records.ListRecords(ctx, strings.TrimSpace(objectKey), recordmodel.RecordListQuery{
		Page: page, PageSize: pageSize, Search: strings.TrimSpace(query.Search), SearchFields: append([]string(nil), query.SearchFields...),
		Filters: cloneAnyMap(query.Filters), Sort: sorts, SelectFields: append([]string(nil), query.SelectFields...), AfterID: strings.TrimSpace(query.AfterID),
	}, principal)
	if serviceErr != nil {
		return runtimeengine.Page{}, projectEngineError(serviceErr)
	}
	items := make([]runtimeengine.Record, 0, len(result.Items))
	for _, record := range result.Items {
		items = append(items, projectEngineRecord(strings.TrimSpace(objectKey), record))
	}
	return runtimeengine.Page{Items: items, Page: result.Page, PageSize: result.PageSize, Total: result.Total, HasNext: result.HasNext, NextAfterID: result.NextAfterID}, nil
}

func (e *projectEngine) Get(ctx context.Context, objectKey, recordID string) (runtimeengine.Record, error) {
	principal, err := e.requestPrincipal(ctx)
	if err != nil {
		return runtimeengine.Record{}, err
	}
	record, serviceErr := e.records.GetRecord(ctx, strings.TrimSpace(objectKey), strings.TrimSpace(recordID), principal)
	if serviceErr != nil {
		return runtimeengine.Record{}, projectEngineError(serviceErr)
	}
	return projectEngineRecord(strings.TrimSpace(objectKey), record), nil
}

func (e *projectEngine) Create(ctx context.Context, objectKey string, value any) (runtimeengine.Record, error) {
	principal, err := e.requestPrincipal(ctx)
	if err != nil {
		return runtimeengine.Record{}, err
	}
	fields, err := projectEngineFields(value)
	if err != nil {
		return runtimeengine.Record{}, err
	}
	record, serviceErr := e.records.CreateRecord(projectMutationContext(ctx), strings.TrimSpace(objectKey), fields, principal)
	if serviceErr != nil {
		return runtimeengine.Record{}, projectEngineError(serviceErr)
	}
	return projectEngineRecord(strings.TrimSpace(objectKey), record), nil
}

func (e *projectEngine) Update(ctx context.Context, objectKey, recordID string, value any) (runtimeengine.Record, error) {
	principal, err := e.requestPrincipal(ctx)
	if err != nil {
		return runtimeengine.Record{}, err
	}
	patch, err := projectEngineFields(value)
	if err != nil {
		return runtimeengine.Record{}, err
	}
	record, serviceErr := e.records.UpdateRecord(projectMutationContext(ctx), strings.TrimSpace(objectKey), strings.TrimSpace(recordID), patch, principal)
	if serviceErr != nil {
		return runtimeengine.Record{}, projectEngineError(serviceErr)
	}
	return projectEngineRecord(strings.TrimSpace(objectKey), record), nil
}

func (e *projectEngine) Delete(ctx context.Context, objectKey, recordID string) error {
	principal, err := e.requestPrincipal(ctx)
	if err != nil {
		return err
	}
	if serviceErr := e.records.DeleteRecord(projectMutationContext(ctx), strings.TrimSpace(objectKey), strings.TrimSpace(recordID), principal); serviceErr != nil {
		return projectEngineError(serviceErr)
	}
	return nil
}

func projectMutationContext(ctx context.Context) context.Context {
	return recordmutation.WithMutationInvocation(ctx, recordmutation.MutationInvocation{Source: transactionmodel.MutationSourceProjectHTTP})
}

func (e *projectEngine) InvokeAction(ctx context.Context, request runtimeengine.ActionRequest) (runtimeengine.ActionResult, error) {
	principal, err := e.requestPrincipal(ctx)
	if err != nil {
		return runtimeengine.ActionResult{}, err
	}
	if e.actions == nil {
		return runtimeengine.ActionResult{}, runtimeengine.NewError(runtimeengine.ErrorUnavailable, "backend.action.unavailable", nil, nil)
	}
	input, err := projectEngineFields(request.Input)
	if err != nil {
		return runtimeengine.ActionResult{}, err
	}
	actionContext := actionapplication.WithProjectNativeInput(ctx, request.ActionKey, request.Input)
	result, serviceErr := e.actions.Invoke(actionContext, actionmodel.ActionSourceHTTP, actionmodel.ActionInvocation{
		ActionKey: strings.TrimSpace(request.ActionKey), ObjectKey: strings.TrimSpace(request.ObjectKey), RecordID: strings.TrimSpace(request.RecordID),
		Input: input, Principal: principal, Actor: principal, RequestID: principal.RequestID,
		IdempotencyKey: strings.TrimSpace(request.IdempotencyKey), TargetOrganizationID: strings.TrimSpace(request.TargetOrganizationID), AssuranceToken: strings.TrimSpace(request.AssuranceToken),
	})
	if serviceErr != nil {
		return runtimeengine.ActionResult{}, projectEngineError(serviceErr)
	}
	projected := runtimeengine.ActionResult{InvocationID: result.InvocationID, Status: result.Status, Output: projectEngineActionOutput(result)}
	if result.Record != nil {
		record := projectEngineRecord(result.Record.ObjectKey, result.Record.Record)
		projected.Record = &record
	}
	return projected, nil
}

func projectEngineActionOutput(result actionmodel.ActionInvocationResult) map[string]any {
	if result.Record != nil {
		return cloneAnyMap(result.Record.Output)
	}
	if result.Object != nil {
		return cloneAnyMap(result.Object.Output)
	}
	if data, ok := result.Output["data"].(map[string]any); ok {
		return cloneAnyMap(data)
	}
	return cloneAnyMap(result.Output)
}

func projectEngineFields(value any) (map[string]any, error) {
	if value == nil {
		return map[string]any{}, nil
	}
	return runtimeengine.EncodeFields(value)
}

func (e *projectEngine) requestPrincipal(ctx context.Context) (principalmodel.Principal, error) {
	if e == nil || e.principal == nil {
		return principalmodel.Principal{}, runtimeengine.NewError(runtimeengine.ErrorUnavailable, "backend.project_engine.unavailable", nil, nil)
	}
	principal, ok := e.principal(ctx)
	if !ok || !principal.Known {
		return principalmodel.Principal{}, runtimeengine.NewError(runtimeengine.ErrorUnauthenticated, "auth.session_expired", nil, nil)
	}
	return principal, nil
}

func projectEngineRecord(objectKey string, record recordmodel.Record) runtimeengine.Record {
	return runtimeengine.Record{
		ID: record.ID, ObjectKey: strings.TrimSpace(objectKey), Fields: cloneAnyMap(record.Data), CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
		OwnerUserID: record.OwnerUserID, OwnerUserName: record.OwnerUserName,
	}
}

func projectEngineError(err error) error {
	kind := runtimeengine.ErrorInternal
	switch apperror.KindOf(err) {
	case apperror.KindBadRequest:
		kind = runtimeengine.ErrorBadRequest
	case apperror.KindForbidden:
		kind = runtimeengine.ErrorForbidden
	case apperror.KindNotFound:
		kind = runtimeengine.ErrorNotFound
	case apperror.KindConflict:
		kind = runtimeengine.ErrorConflict
	case apperror.KindRateLimited:
		kind = runtimeengine.ErrorRateLimited
	case apperror.KindUnavailable:
		kind = runtimeengine.ErrorUnavailable
	}
	return runtimeengine.NewError(kind, apperror.CodeOf(err), apperror.ParamsOf(err), err)
}

func cloneAnyMap(source map[string]any) map[string]any {
	if source == nil {
		return nil
	}
	result := make(map[string]any, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
