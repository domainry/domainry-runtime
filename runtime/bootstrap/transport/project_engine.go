package transport

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
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
	assurance     *actionapplication.ActionAssuranceApplicationService
	accountReads  integrationsdk.ConnectionAccountReads
	accountWrites integrationsdk.ConnectionAccountWrites
	identityUsers identitysdk.Projection
	knowledge     agentsdk.ConversationLibraryKnowledgeSource
	runtimeID     string
	principal     func(context.Context) (principalmodel.Principal, bool)
}

func newProjectEngine(
	records *recordapplication.RecordApplicationService,
	actions *actionapplication.ActionApplicationService,
	assurance *actionapplication.ActionAssuranceApplicationService,
	accountReads integrationsdk.ConnectionAccountReads,
	accountWrites integrationsdk.ConnectionAccountWrites,
	identityUsers identitysdk.Projection,
	knowledge agentsdk.ConversationLibraryKnowledgeSource,
	runtimeID string,
	principal func(context.Context) (principalmodel.Principal, bool),
) runtimeengine.Engine {
	return &projectEngine{records: records, actions: actions, assurance: assurance, accountReads: accountReads, accountWrites: accountWrites, identityUsers: identityUsers, knowledge: knowledge, runtimeID: strings.TrimSpace(runtimeID), principal: principal}
}

func (e *projectEngine) SearchKnowledgeLibrary(ctx context.Context, libraryID, query string) (runtimeengine.KnowledgeSearchResult, error) {
	principal, err := e.requestPrincipal(ctx)
	if err != nil {
		return runtimeengine.KnowledgeSearchResult{}, err
	}
	libraryID, query = strings.TrimSpace(libraryID), strings.TrimSpace(query)
	if e.knowledge == nil || e.runtimeID == "" {
		return runtimeengine.KnowledgeSearchResult{}, runtimeengine.NewError(runtimeengine.ErrorUnavailable, "backend.knowledge.search_unavailable", nil, nil)
	}
	if libraryID == "" || len(libraryID) > 96 || query == "" || len([]rune(query)) > 4096 {
		return runtimeengine.KnowledgeSearchResult{}, runtimeengine.NewError(runtimeengine.ErrorBadRequest, "backend.knowledge.search_request_invalid", nil, nil)
	}
	authority := agentsdk.ConversationAuthority{
		Known: true, RuntimeID: e.runtimeID, WorkspaceID: principal.WorkspaceID,
		UserID: principal.UserID, RoleKey: principal.RoleKey,
	}
	result, searchErr := e.knowledge.SearchLibraryKnowledge(ctx, libraryID, query, authority)
	if searchErr != nil {
		return runtimeengine.KnowledgeSearchResult{}, projectKnowledgeSearchError("backend.knowledge.search_failed", searchErr)
	}
	if result.LibraryID != libraryID || result.Operation != "search" || result.Query != query || result.DocumentID != "" {
		return runtimeengine.KnowledgeSearchResult{}, runtimeengine.NewError(runtimeengine.ErrorUnavailable, "backend.knowledge.search_result_invalid", nil, nil)
	}
	if revalidateErr := e.knowledge.RevalidateKnowledge(ctx, result, authority); revalidateErr != nil {
		return runtimeengine.KnowledgeSearchResult{}, projectKnowledgeSearchError("backend.knowledge.search_revalidation_failed", revalidateErr)
	}
	out := runtimeengine.KnowledgeSearchResult{LibraryID: libraryID, Query: query, Citations: make([]runtimeengine.KnowledgeSearchCitation, 0, len(result.Citations))}
	for _, citation := range result.Citations {
		if citation.LibraryID != libraryID || strings.TrimSpace(citation.ID) == "" || strings.TrimSpace(citation.DocumentID) == "" || citation.Operation != "search" {
			return runtimeengine.KnowledgeSearchResult{}, runtimeengine.NewError(runtimeengine.ErrorUnavailable, "backend.knowledge.search_result_invalid", nil, nil)
		}
		out.Citations = append(out.Citations, runtimeengine.KnowledgeSearchCitation{
			ID: citation.ID, DocumentID: citation.DocumentID, Title: citation.Title,
		})
	}
	return out, nil
}

func projectKnowledgeSearchError(code string, err error) error {
	kind := runtimeengine.ErrorUnavailable
	var agentError *agentsdk.Error
	if errors.As(err, &agentError) {
		switch agentError.Class {
		case "bad_request":
			kind = runtimeengine.ErrorBadRequest
		case "forbidden", "not_found":
			kind = runtimeengine.ErrorForbidden
		case "conflict":
			kind = runtimeengine.ErrorConflict
		case "rate_limited":
			kind = runtimeengine.ErrorRateLimited
		}
	}
	return runtimeengine.NewError(kind, code, nil, err)
}

func (e *projectEngine) BeginActionAssurance(ctx context.Context, request runtimeengine.ActionAssuranceChallengeRequest) (runtimeengine.ActionAssuranceChallenge, error) {
	principal, err := e.requestPrincipal(ctx)
	if err != nil {
		return runtimeengine.ActionAssuranceChallenge{}, err
	}
	if e.assurance == nil {
		return runtimeengine.ActionAssuranceChallenge{}, runtimeengine.NewError(runtimeengine.ErrorUnavailable, "backend.action.assurance_unavailable", nil, nil)
	}
	challenge, serviceErr := e.assurance.Begin(ctx, actionapplication.ActionAssuranceChallengeRequest{
		ActionKey: strings.TrimSpace(request.ActionKey), ObjectKey: strings.TrimSpace(request.ObjectKey), RecordID: strings.TrimSpace(request.RecordID), Payload: cloneAnyMap(request.Payload),
	}, strings.TrimSpace(request.AccessToken), principal)
	if serviceErr != nil {
		return runtimeengine.ActionAssuranceChallenge{}, projectEngineError(serviceErr)
	}
	return runtimeengine.ActionAssuranceChallenge{
		Provider: challenge.Provider, State: challenge.State, Type: challenge.Type, Purpose: challenge.Purpose,
		Status: string(challenge.Status), MaskedDestination: challenge.MaskedDestination, RetryAt: challenge.RetryAt, ExpiresAt: challenge.ExpiresAt,
	}, nil
}

func (e *projectEngine) VerifyActionAssurance(ctx context.Context, request runtimeengine.ActionAssuranceVerificationRequest) (runtimeengine.ActionAssuranceGrant, error) {
	principal, err := e.requestPrincipal(ctx)
	if err != nil {
		return runtimeengine.ActionAssuranceGrant{}, err
	}
	if e.assurance == nil {
		return runtimeengine.ActionAssuranceGrant{}, runtimeengine.NewError(runtimeengine.ErrorUnavailable, "backend.action.assurance_unavailable", nil, nil)
	}
	result, serviceErr := e.assurance.Verify(ctx, actionapplication.ActionAssuranceVerificationRequest{
		ActionAssuranceChallengeRequest: actionapplication.ActionAssuranceChallengeRequest{
			ActionKey: strings.TrimSpace(request.ActionKey), ObjectKey: strings.TrimSpace(request.ObjectKey), RecordID: strings.TrimSpace(request.RecordID), Payload: cloneAnyMap(request.Payload),
		},
		Provider: strings.TrimSpace(request.Provider), State: strings.TrimSpace(request.State), Code: strings.TrimSpace(request.Code),
	}, strings.TrimSpace(request.AccessToken), principal)
	if serviceErr != nil {
		return runtimeengine.ActionAssuranceGrant{}, projectEngineError(serviceErr)
	}
	return runtimeengine.ActionAssuranceGrant{
		AssuranceToken: result.AssuranceToken, GrantID: result.GrantID, Methods: append([]string(nil), result.Methods...), ExpiresAt: result.ExpiresAt,
	}, nil
}

func (e *projectEngine) ReadConnectionAccount(ctx context.Context, connectionKey, operationKey, contractSHA256 string, payload any) (runtimeengine.ConnectionAccountReadResult, error) {
	principal, err := e.requestPrincipal(ctx)
	if err != nil {
		return runtimeengine.ConnectionAccountReadResult{}, err
	}
	if e.accountReads == nil {
		return runtimeengine.ConnectionAccountReadResult{}, runtimeengine.NewError(runtimeengine.ErrorUnavailable, "backend.connector.account_read_unavailable", nil, nil)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return runtimeengine.ConnectionAccountReadResult{}, runtimeengine.NewError(runtimeengine.ErrorBadRequest, "backend.connector.account_read_payload_invalid", nil, err)
	}
	subject := integrationsdk.ConnectionAccountSubject{
		WorkspaceID: principal.WorkspaceID, UserID: principal.UserID,
		Access: integrationsdk.ConnectionAccountAccess{Personal: true},
	}
	operation := integrationsdk.ConnectionAccountReadOperation{Operation: strings.TrimSpace(operationKey), ContractSHA256: strings.TrimSpace(contractSHA256)}
	access, err := e.accountReads.AuthorizeConnectionAccountRead(ctx, subject, strings.TrimSpace(connectionKey), operation)
	if err != nil {
		return runtimeengine.ConnectionAccountReadResult{}, runtimeengine.NewError(runtimeengine.ErrorForbidden, "backend.connector.account_read_denied", nil, err)
	}
	requestID := requestcontext.RequestID(ctx)
	if requestID == "" {
		requestID = requestcontext.NewRequestID()
	}
	result, err := e.accountReads.ReadConnectionAccount(ctx, subject, access.Source.ConnectionKey, integrationsdk.ConnectionAccountReadRequest{
		RequestID: requestID, Operation: operation.Operation, ContractSHA256: operation.ContractSHA256, Payload: raw,
	})
	if err != nil {
		return runtimeengine.ConnectionAccountReadResult{}, runtimeengine.NewError(runtimeengine.ErrorUnavailable, "backend.connector.account_read_failed", nil, err)
	}
	if !result.PayloadAvailable || len(result.Payload) == 0 || result.Source != access.Source {
		return runtimeengine.ConnectionAccountReadResult{}, runtimeengine.NewError(runtimeengine.ErrorUnavailable, "backend.connector.account_read_result_unavailable", nil, nil)
	}
	return runtimeengine.ConnectionAccountReadResult{
		ConnectionKey: result.Source.ConnectionKey, ProviderKey: result.Source.ProviderKey,
		Payload: append(json.RawMessage(nil), result.Payload...),
	}, nil
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
	objectKey = strings.TrimSpace(objectKey)
	page := query.Page
	if page <= 0 {
		page = 1
	}
	pageSize := query.PageSize
	if pageSize <= 0 {
		pageSize = 50
	}
	sorts := projectRecordCursorSorts(query.Sorts)
	recordQuery := recordmodel.RecordListQuery{
		Page: page, PageSize: pageSize, Search: strings.TrimSpace(query.Search), SearchFields: append([]string(nil), query.SearchFields...),
		Filters: cloneAnyMap(query.Filters), Sort: sorts, SelectFields: append([]string(nil), query.SelectFields...),
	}
	cursorMode := projectRecordCursorRequired(sorts)
	var cursor projectRecordCursor
	if !cursorMode {
		recordQuery.AfterID = strings.TrimSpace(query.AfterID)
	} else {
		// The sort fields are projected only so persistence can retain their
		// typed NULL-aware values for the next cursor. Public field projection is
		// restored below before records cross the project boundary.
		recordQuery.StableNullsLast = true
		if len(query.SelectFields) > 0 {
			selected := map[string]bool{}
			for _, field := range recordQuery.SelectFields {
				selected[strings.TrimSpace(field)] = true
			}
			for _, sortRule := range sorts {
				if !selected[sortRule.Field] {
					recordQuery.SelectFields = append(recordQuery.SelectFields, sortRule.Field)
					selected[sortRule.Field] = true
				}
			}
		}
		if rawCursor := strings.TrimSpace(query.AfterID); rawCursor != "" {
			cursor, err = decodeProjectRecordCursor(rawCursor, e.runtimeID, objectKey, query, principal, sorts)
			if err != nil {
				return runtimeengine.Page{}, err
			}
			recordQuery.FilterExpression, err = projectRecordCursorFilter(cursor, sorts)
			if err != nil {
				return runtimeengine.Page{}, err
			}
			recordQuery.Page, recordQuery.SkipTotal = 1, true
		} else if page > 1 {
			return runtimeengine.Page{}, projectRecordCursorInvalid(nil)
		}
	}
	result, serviceErr := e.records.ListRecords(ctx, objectKey, recordQuery, principal)
	if serviceErr != nil {
		return runtimeengine.Page{}, projectEngineError(serviceErr)
	}
	nextAfterID := result.NextAfterID
	if cursorMode {
		if cursor.Total > 0 || strings.TrimSpace(query.AfterID) != "" {
			result.Total = cursor.Total
		}
		result.Page = page
		if strings.TrimSpace(query.AfterID) != "" {
			result.Page = cursor.Page
		}
		nextAfterID = ""
		if result.HasNext && len(result.Items) > 0 {
			nextAfterID, err = encodeProjectRecordCursor(e.runtimeID, objectKey, query, principal, sorts, result.Total, result.Page+1, result.Items[len(result.Items)-1])
			if err != nil {
				return runtimeengine.Page{}, err
			}
		}
	}
	items := make([]runtimeengine.Record, 0, len(result.Items))
	for _, record := range result.Items {
		item := projectEngineRecord(objectKey, record)
		if cursorMode && len(query.SelectFields) > 0 {
			selected := map[string]bool{}
			for _, field := range query.SelectFields {
				selected[strings.TrimSpace(field)] = true
			}
			for field := range item.Fields {
				if !selected[field] {
					delete(item.Fields, field)
				}
			}
		}
		items = append(items, item)
	}
	return runtimeengine.Page{Items: items, Page: result.Page, PageSize: result.PageSize, Total: result.Total, HasNext: result.HasNext, NextAfterID: nextAfterID}, nil
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

func (e *projectEngine) CreateRecordIdempotent(ctx context.Context, objectKey string, value any, idempotencyKey string) (runtimeengine.Record, bool, error) {
	principal, err := e.requestPrincipal(ctx)
	if err != nil {
		return runtimeengine.Record{}, false, err
	}
	fields, err := projectEngineFields(value)
	if err != nil {
		return runtimeengine.Record{}, false, err
	}
	record, replayed, serviceErr := e.records.CreateRecordIdempotentResult(projectMutationContext(ctx), strings.TrimSpace(objectKey), fields, strings.TrimSpace(idempotencyKey), principal)
	if serviceErr != nil {
		return runtimeengine.Record{}, false, projectEngineError(serviceErr)
	}
	return projectEngineRecord(strings.TrimSpace(objectKey), record), replayed, nil
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

func (e *projectEngine) UpdateRecordIdempotent(ctx context.Context, objectKey, recordID string, value any, idempotencyKey string) (runtimeengine.Record, error) {
	principal, err := e.requestPrincipal(ctx)
	if err != nil {
		return runtimeengine.Record{}, err
	}
	patch, err := projectEngineFields(value)
	if err != nil {
		return runtimeengine.Record{}, err
	}
	record, serviceErr := e.records.UpdateRecordIdempotent(projectMutationContext(ctx), strings.TrimSpace(objectKey), strings.TrimSpace(recordID), patch, strings.TrimSpace(idempotencyKey), principal)
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

func (e *projectEngine) DeleteRecordIdempotent(ctx context.Context, objectKey, recordID, expectedUpdatedAt, idempotencyKey string) (bool, error) {
	principal, err := e.requestPrincipal(ctx)
	if err != nil {
		return false, err
	}
	replayed, serviceErr := e.records.DeleteRecordExpectedIdempotent(projectMutationContext(ctx), strings.TrimSpace(objectKey), strings.TrimSpace(recordID), strings.TrimSpace(expectedUpdatedAt), strings.TrimSpace(idempotencyKey), principal)
	if serviceErr != nil {
		return false, projectEngineError(serviceErr)
	}
	return replayed, nil
}

func (e *projectEngine) PreviewRecordImport(ctx context.Context, objectKey string, rawCSV []byte) (runtimeengine.RecordImportPreview, error) {
	principal, err := e.requestPrincipal(ctx)
	if err != nil {
		return runtimeengine.RecordImportPreview{}, err
	}
	preview, serviceErr := e.records.PreviewImport(ctx, strings.TrimSpace(objectKey), rawCSV, principal)
	if serviceErr != nil {
		return runtimeengine.RecordImportPreview{}, projectEngineError(serviceErr)
	}
	return projectEngineImportPreview(preview), nil
}

func (e *projectEngine) ApplyRecordImportIdempotent(ctx context.Context, objectKey string, rawCSV []byte, idempotencyKey string) (runtimeengine.RecordImportApplyResult, bool, error) {
	principal, err := e.requestPrincipal(ctx)
	if err != nil {
		return runtimeengine.RecordImportApplyResult{}, false, err
	}
	result, replayed, serviceErr := e.records.ApplyImportIdempotent(ctx, strings.TrimSpace(objectKey), rawCSV, strings.TrimSpace(idempotencyKey), principal)
	if serviceErr != nil {
		return runtimeengine.RecordImportApplyResult{}, false, projectEngineError(serviceErr)
	}
	return runtimeengine.RecordImportApplyResult{
		ObjectKey: result.ObjectKey, Created: result.Created, Skipped: result.Skipped, Preview: projectEngineImportPreview(result.Preview),
	}, replayed, nil
}

func (e *projectEngine) EnqueueRecordImport(ctx context.Context, objectKey string, rawCSV []byte, idempotencyKey string) (runtimeengine.RecordBatchJob, bool, error) {
	principal, err := e.requestPrincipal(ctx)
	if err != nil {
		return runtimeengine.RecordBatchJob{}, false, err
	}
	job, replayed, serviceErr := e.records.EnqueueImportJob(ctx, strings.TrimSpace(objectKey), rawCSV, strings.TrimSpace(idempotencyKey), principal)
	if serviceErr != nil {
		return runtimeengine.RecordBatchJob{}, false, projectEngineError(serviceErr)
	}
	return projectEngineBatchJob(job), replayed, nil
}

func (e *projectEngine) DispatchRecordExportIdempotent(ctx context.Context, objectKey, idempotencyKey string, options runtimeengine.RecordExportOptions) (runtimeengine.RecordExportDispatch, error) {
	principal, err := e.requestPrincipal(ctx)
	if err != nil {
		return runtimeengine.RecordExportDispatch{}, err
	}
	sorts := make([]recordmodel.RecordSortRule, 0, len(options.Query.Sorts))
	for _, sortRule := range options.Query.Sorts {
		sorts = append(sorts, recordmodel.RecordSortRule{Field: strings.TrimSpace(sortRule.Field), Direction: strings.TrimSpace(sortRule.Direction)})
	}
	dispatch, serviceErr := e.records.DispatchExportIdempotent(ctx, strings.TrimSpace(objectKey), strings.TrimSpace(idempotencyKey), recordapplication.RecordExportOptions{
		Fields: append([]string(nil), options.Fields...), Reason: strings.TrimSpace(options.Reason), MaskingPolicy: strings.TrimSpace(options.MaskingPolicy),
		FilterSummary: strings.TrimSpace(options.FilterSummary), AssuranceToken: strings.TrimSpace(options.AssuranceToken),
		Query: recordmodel.RecordListQuery{
			Page: options.Query.Page, PageSize: options.Query.PageSize, Search: strings.TrimSpace(options.Query.Search), SearchFields: append([]string(nil), options.Query.SearchFields...),
			Filters: cloneAnyMap(options.Query.Filters), Sort: sorts, SelectFields: append([]string(nil), options.Query.SelectFields...), AfterID: strings.TrimSpace(options.Query.AfterID),
		},
	}, principal)
	if serviceErr != nil {
		return runtimeengine.RecordExportDispatch{}, projectEngineError(serviceErr)
	}
	return runtimeengine.RecordExportDispatch{
		Delivery: dispatch.Delivery, Content: append([]byte(nil), dispatch.Content...), Filename: dispatch.Filename,
		Job: projectEngineBatchJob(dispatch.Job), Replayed: dispatch.Replayed,
	}, nil
}

func (e *projectEngine) DownloadRecordExport(ctx context.Context, jobID string) (runtimeengine.RecordExportArtifact, error) {
	principal, err := e.requestPrincipal(ctx)
	if err != nil {
		return runtimeengine.RecordExportArtifact{}, err
	}
	artifact, serviceErr := e.records.DownloadExport(ctx, strings.TrimSpace(jobID), principal)
	if serviceErr != nil {
		return runtimeengine.RecordExportArtifact{}, projectEngineError(serviceErr)
	}
	return runtimeengine.RecordExportArtifact{
		ID: artifact.ID, Filename: artifact.Filename, ContentType: artifact.ContentType, SHA256: artifact.SHA256,
		Size: artifact.Size, ExpiresAt: artifact.ExpiresAt.Format("2006-01-02T15:04:05.999999999Z07:00"), Content: artifact.Content,
	}, nil
}

func projectEngineImportPreview(source recordmodel.RecordImportPreview) runtimeengine.RecordImportPreview {
	projected := runtimeengine.RecordImportPreview{
		ObjectKey: source.ObjectKey, ValidRows: source.ValidRows, InvalidRows: source.InvalidRows, DuplicateRows: source.DuplicateRows, CanApply: source.CanApply,
		Rows: make([]runtimeengine.RecordImportPreviewRow, 0, len(source.Rows)), ErrorRows: make([]runtimeengine.RecordImportPreviewRow, 0, len(source.ErrorRows)),
	}
	projectRow := func(row recordmodel.RecordImportPreviewRow) runtimeengine.RecordImportPreviewRow {
		issues := make([]runtimeengine.RecordImportRowIssue, 0, len(row.Issues))
		for _, issue := range row.Issues {
			issues = append(issues, runtimeengine.RecordImportRowIssue{Field: issue.Field, Message: issue.Message, Code: issue.Code, Params: cloneStringMap(issue.Params), Severity: issue.Severity})
		}
		return runtimeengine.RecordImportPreviewRow{
			Row: row.Row, Data: cloneAnyMap(row.Data), RawValues: cloneStringMap(row.RawValues), Issues: issues,
			ErrorSummary: row.ErrorSummary, Valid: row.Valid, Duplicate: row.Duplicate,
		}
	}
	for _, row := range source.Rows {
		projected.Rows = append(projected.Rows, projectRow(row))
	}
	for _, row := range source.ErrorRows {
		projected.ErrorRows = append(projected.ErrorRows, projectRow(row))
	}
	return projected
}

func projectEngineBatchJob(job recordmodel.RecordBatchJob) runtimeengine.RecordBatchJob {
	return runtimeengine.RecordBatchJob{
		ID: job.ID, WorkspaceID: job.WorkspaceID, Kind: job.Kind, ObjectKey: job.ObjectKey, Status: job.Status,
		Checkpoint: job.Checkpoint, Total: job.Total, ResultFilename: job.ResultFilename, ResultType: job.ResultType,
		ResultArtifactID: job.ResultArtifactID, ErrorCode: job.ErrorCode, ActorID: job.ActorID, RoleKey: job.RoleKey,
		CreatedAt: job.CreatedAt, UpdatedAt: job.UpdatedAt,
	}
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

func cloneStringMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
