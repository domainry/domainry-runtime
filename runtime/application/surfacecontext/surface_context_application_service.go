package surfacecontext

import (
	identitysdk "github.com/domainry/domainry-identity-sdk"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	reportmodel "github.com/domainry/domainry-report-sdk/model"

	surfacecontextmodel "github.com/domainry/domainry-runtime/runtime/domain/surfacecontext/model"

	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

const maxObjects = 24
const defaultPageSize = 8
const maxPageSize = 50

type SurfaceContextDependencies struct {
	Objects            func() map[string]definitionmodel.ObjectSchema
	Reports            func() []reportmodel.ReportSchema
	ListRecords        func(context.Context, string, recordmodel.RecordListQuery, principalmodel.Principal) (recordmodel.RecordPageResult, error)
	GetRecord          func(context.Context, string, string, principalmodel.Principal) (recordmodel.Record, error)
	ListStoredRecords  func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error)
	ListDirectoryUsers func(context.Context) ([]identitysdk.User, error)
}

// SurfaceContextApplicationService resolves context exposed to runtime surfaces.
type SurfaceContextApplicationService struct {
	dependencies SurfaceContextDependencies
}

func NewSurfaceContextApplicationService(dependencies SurfaceContextDependencies) *SurfaceContextApplicationService {
	return &SurfaceContextApplicationService{dependencies: dependencies}
}

func (s *SurfaceContextApplicationService) Context(ctx context.Context, request surfacecontextmodel.SurfaceContextRequest, principal principalmodel.Principal) surfacecontextmodel.SurfaceContextResult {
	schema := s.objects()
	result := surfacecontextmodel.SurfaceContextResult{
		SurfaceKey:     strings.TrimSpace(request.SurfaceKey),
		Objects:        map[string]surfacecontextmodel.SurfaceContextObjectResult{},
		RelationLabels: map[string]map[string]string{},
		Metrics:        map[string]any{},
		Permissions:    map[string]any{},
	}
	permissions := map[string]any{}
	maskedFieldCount := 0
	for index, item := range request.Objects {
		if index >= maxObjects {
			break
		}
		objectKey := strings.TrimSpace(item.ObjectKey)
		if objectKey == "" {
			continue
		}
		if _, exists := result.Objects[objectKey]; exists {
			continue
		}
		pageSize := item.PageSize
		if pageSize <= 0 {
			pageSize = defaultPageSize
		}
		if pageSize > maxPageSize {
			pageSize = maxPageSize
		}
		pageNumber := item.Page
		if pageNumber <= 0 {
			pageNumber = 1
		}
		page := recordmodel.RecordPageResult{Items: []recordmodel.Record{}, Page: pageNumber, PageSize: pageSize}
		var err error
		if s.dependencies.ListRecords == nil {
			err = fmt.Errorf("surface context record query unavailable")
		} else {
			page, err = s.dependencies.ListRecords(ctx, objectKey, recordmodel.RecordListQuery{
				Page:         pageNumber,
				PageSize:     pageSize,
				Search:       strings.TrimSpace(item.Search),
				SearchFields: append([]string(nil), item.SearchFields...),
				Filters:      item.Filters,
				Sort:         item.Sort,
			}, principal)
		}
		objectResult := surfacecontextmodel.SurfaceContextObjectResult{ObjectKey: objectKey, Page: page}
		if err != nil {
			objectResult.Page = recordmodel.RecordPageResult{Items: []recordmodel.Record{}, Page: pageNumber, PageSize: pageSize}
			objectResult.Error = err.Error()
			permissions[objectKey] = map[string]any{"read": false, "error_code": apperror.CodeOf(err)}
			result.Diagnostics.DeniedObjectCount++
		} else {
			result.Diagnostics.ReadableObjectCount++
			if object, ok := schema[objectKey]; ok {
				masked := SurfaceContextMaskedFields(principal, object)
				maskedFieldCount += len(masked)
				permissions[objectKey] = map[string]any{"read": true, "masked_fields": masked}
			} else {
				permissions[objectKey] = map[string]any{"read": true}
			}
		}
		result.Objects[objectKey] = objectResult
	}
	if selected := s.selectedRecord(ctx, request, principal); selected != nil {
		result.Selected = selected
	}
	result.RelationProjections, result.RelationLabels = s.relationProjections(ctx, result.Objects, principal)
	result.Metrics = SurfaceContextMetrics(result.Objects)
	result.Reports = s.reports(result.Objects)
	result.Permissions = permissions
	result.Diagnostics.RequestedObjectCount = len(request.Objects)
	result.Diagnostics.RelationLabelCount = SurfaceContextRelationLabelCount(result.RelationLabels)
	result.Diagnostics.MaskedFieldCount = maskedFieldCount
	result.Diagnostics.Include = append([]string(nil), request.Include...)
	return result
}

type relationRequest struct {
	sourceObjectKey string
	sourceRecordID  string
	fieldKey        string
	targetObjectKey string
	targetRecordID  string
}

func (s *SurfaceContextApplicationService) relationProjections(ctx context.Context, objects map[string]surfacecontextmodel.SurfaceContextObjectResult, principal principalmodel.Principal) ([]surfacecontextmodel.RelationProjection, map[string]map[string]string) {
	schema := s.objects()
	requests := []relationRequest{}
	targetIDs := map[string]map[string]bool{}
	for sourceKey, objectResult := range objects {
		source, ok := schema[sourceKey]
		if !ok {
			continue
		}
		for _, field := range source.Fields {
			if strings.TrimSpace(field.Type) != "relation" {
				continue
			}
			targetKey := recordvalidation.RecordRelationTarget(field)
			if targetKey == "" {
				continue
			}
			for _, record := range objectResult.Page.Items {
				targetID := strings.TrimSpace(fmt.Sprint(record.Data[field.Key]))
				if targetID == "" || targetID == "<nil>" {
					continue
				}
				requests = append(requests, relationRequest{sourceObjectKey: sourceKey, sourceRecordID: record.ID, fieldKey: field.Key, targetObjectKey: targetKey, targetRecordID: targetID})
				if targetIDs[targetKey] == nil {
					targetIDs[targetKey] = map[string]bool{}
				}
				targetIDs[targetKey][targetID] = true
			}
		}
	}

	targets := map[string]map[string]surfacecontextmodel.RelationProjection{}
	for targetKey, ids := range targetIDs {
		targets[targetKey] = map[string]surfacecontextmodel.RelationProjection{}
		if targetKey == "identity_user" {
			canReadDirectory := principal.HasPermission("workspace.admin") || principal.HasPermission("identity.users.read")
			if s.dependencies.ListDirectoryUsers == nil || (!canReadDirectory && !ids[principal.UserID]) {
				continue
			}
			users, err := s.dependencies.ListDirectoryUsers(ctx)
			if err != nil {
				continue
			}
			for _, user := range users {
				if ids[user.ID] && (canReadDirectory || user.ID == principal.UserID) {
					targets[targetKey][user.ID] = surfacecontextmodel.RelationProjection{TargetObjectKey: targetKey, TargetRecordID: user.ID, DisplayField: "name", DisplayValue: SurfaceContextValueOrDefault(strings.TrimSpace(user.Name), user.ID), DetailRoute: "/security/accounts/" + url.PathEscape(user.ID), Mode: "readable"}
				}
			}
			continue
		}
		idValues := SurfaceContextSortedIDs(ids)
		if s.dependencies.ListRecords == nil {
			continue
		}
		page, err := s.dependencies.ListRecords(ctx, targetKey, recordmodel.RecordListQuery{Page: 1, PageSize: min(len(idValues), 200), Filters: map[string]any{"id__in": idValues}}, principal)
		if err != nil {
			targets[targetKey] = s.labelOnlyTargets(ctx, schema, requests, targetKey, ids, principal)
			continue
		}
		targetObject := schema[targetKey]
		for _, record := range page.Items {
			displayField, displayValue := SurfaceContextRecordDisplay(targetObject, record)
			targets[targetKey][record.ID] = surfacecontextmodel.RelationProjection{TargetObjectKey: targetKey, TargetRecordID: record.ID, DisplayField: displayField, DisplayValue: displayValue, DetailRoute: "/objects/" + url.PathEscape(targetKey) + "/records/" + url.PathEscape(record.ID), Mode: "readable"}
		}
	}

	projections := []surfacecontextmodel.RelationProjection{}
	labels := map[string]map[string]string{}
	for _, request := range requests {
		projection, readable := targets[request.targetObjectKey][request.targetRecordID]
		if !readable {
			continue
		}
		projection.SourceObjectKey = request.sourceObjectKey
		projection.SourceRecordID = request.sourceRecordID
		projection.FieldKey = request.fieldKey
		projections = append(projections, projection)
		if labels[projection.TargetObjectKey] == nil {
			labels[projection.TargetObjectKey] = map[string]string{}
		}
		labels[projection.TargetObjectKey][projection.TargetRecordID] = projection.DisplayValue
	}
	sort.Slice(projections, func(i, j int) bool {
		left := projections[i].SourceObjectKey + "\x00" + projections[i].SourceRecordID + "\x00" + projections[i].FieldKey
		right := projections[j].SourceObjectKey + "\x00" + projections[j].SourceRecordID + "\x00" + projections[j].FieldKey
		return left < right
	})
	return projections, labels
}

func (s *SurfaceContextApplicationService) labelOnlyTargets(ctx context.Context, schema map[string]definitionmodel.ObjectSchema, requests []relationRequest, targetKey string, ids map[string]bool, principal principalmodel.Principal) map[string]surfacecontextmodel.RelationProjection {
	targetObject, ok := schema[targetKey]
	if !ok || s.dependencies.ListStoredRecords == nil {
		return map[string]surfacecontextmodel.RelationProjection{}
	}
	displayFields := map[string][]string{}
	for _, request := range requests {
		if request.targetObjectKey != targetKey || !ids[request.targetRecordID] {
			continue
		}
		permission, allowed := SurfaceContextReferencePermission(principal, request.sourceObjectKey, request.fieldKey, targetKey)
		if allowed {
			displayFields[request.targetRecordID] = SurfaceContextAppendReferenceDisplayFields(displayFields[request.targetRecordID], permission.DisplayFields)
		}
	}
	if len(displayFields) == 0 {
		return map[string]surfacecontextmodel.RelationProjection{}
	}
	idValues := make([]string, 0, len(displayFields))
	for id := range displayFields {
		idValues = append(idValues, id)
	}
	sort.Strings(idValues)
	page, err := s.dependencies.ListStoredRecords(ctx, principal.WorkspaceID, targetObject, recordmodel.RecordListQuery{Page: 1, PageSize: min(len(idValues), 200), Filters: map[string]any{"id__in": idValues}})
	if err != nil {
		return map[string]surfacecontextmodel.RelationProjection{}
	}
	out := map[string]surfacecontextmodel.RelationProjection{}
	for _, record := range page.Items {
		displayField, displayValue := SurfaceContextPermittedRecordDisplay(targetObject, record, displayFields[record.ID])
		out[record.ID] = surfacecontextmodel.RelationProjection{TargetObjectKey: targetKey, TargetRecordID: record.ID, DisplayField: displayField, DisplayValue: displayValue, Mode: "label_only"}
	}
	return out
}

func (s *SurfaceContextApplicationService) selectedRecord(ctx context.Context, request surfacecontextmodel.SurfaceContextRequest, principal principalmodel.Principal) *surfacecontextmodel.SurfaceContextSelectedResult {
	objectKey := strings.TrimSpace(request.SelectedObjectKey)
	recordID := strings.TrimSpace(request.SelectedRecordID)
	if objectKey == "" || recordID == "" {
		return nil
	}
	if s.dependencies.GetRecord == nil {
		return &surfacecontextmodel.SurfaceContextSelectedResult{ObjectKey: objectKey, Error: "surface context record query unavailable"}
	}
	record, err := s.dependencies.GetRecord(ctx, objectKey, recordID, principal)
	if err != nil {
		return &surfacecontextmodel.SurfaceContextSelectedResult{ObjectKey: objectKey, Error: err.Error()}
	}
	return &surfacecontextmodel.SurfaceContextSelectedResult{ObjectKey: objectKey, Record: record}
}

func (s *SurfaceContextApplicationService) reports(objects map[string]surfacecontextmodel.SurfaceContextObjectResult) []map[string]any {
	reportObjects := map[string]struct{}{}
	if s.dependencies.Reports != nil {
		for _, report := range s.dependencies.Reports() {
			for _, objectKey := range SurfaceContextReportSourceObjects(report) {
				reportObjects[objectKey] = struct{}{}
			}
		}
	}
	reportKeys := []string{}
	for objectKey := range objects {
		if _, ok := reportObjects[objectKey]; ok || strings.Contains(objectKey, "report") || strings.Contains(objectKey, "export") || strings.Contains(objectKey, "download") {
			reportKeys = append(reportKeys, objectKey)
		}
	}
	sort.Strings(reportKeys)
	reports := make([]map[string]any, 0, len(reportKeys))
	for _, objectKey := range reportKeys {
		objectResult := objects[objectKey]
		reports = append(reports, map[string]any{"object_key": objectKey, "count": len(objectResult.Page.Items), "total": objectResult.Page.Total})
	}
	return reports
}

func (s *SurfaceContextApplicationService) objects() map[string]definitionmodel.ObjectSchema {
	if s.dependencies.Objects == nil {
		return map[string]definitionmodel.ObjectSchema{}
	}
	return s.dependencies.Objects()
}
