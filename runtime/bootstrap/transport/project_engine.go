package transport

import (
	"context"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
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
	records   *recordapplication.RecordApplicationService
	actions   *actionapplication.ActionApplicationService
	principal func(context.Context) (principalmodel.Principal, bool)
}

func newProjectEngine(
	records *recordapplication.RecordApplicationService,
	actions *actionapplication.ActionApplicationService,
	principal func(context.Context) (principalmodel.Principal, bool),
) runtimeengine.Engine {
	return &projectEngine{records: records, actions: actions, principal: principal}
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
