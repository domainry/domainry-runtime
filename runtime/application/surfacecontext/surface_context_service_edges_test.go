package surfacecontext

import (
	"context"
	"errors"
	"fmt"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
	surfacecontextmodel "github.com/domainry/domainry-runtime/runtime/domain/surfacecontext/model"
)

func TestSurfaceContextRequestNormalizationLimitsErrorsAndSelection(t *testing.T) {
	request := surfacecontextmodel.SurfaceContextRequest{SurfaceKey: " dashboard ", SelectedObjectKey: " customer ", SelectedRecordID: " c1 ", Include: []string{"metrics"}, Objects: []surfacecontextmodel.SurfaceContextObjectRequest{{ObjectKey: " customer ", Page: 0, PageSize: 0}}}
	service := NewSurfaceContextApplicationService(SurfaceContextDependencies{})
	result := service.Context(t.Context(), request, principalmodel.Principal{})
	object := result.Objects["customer"]
	if result.SurfaceKey != "dashboard" || object.Error == "" || object.Page.Page != 1 || object.Page.PageSize != defaultPageSize || result.Diagnostics.DeniedObjectCount != 1 || result.Selected == nil || result.Selected.Error == "" {
		t.Fatalf("result=%#v", result)
	}

	objects := map[string]definitionmodel.ObjectSchema{"customer": {Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "secret"}}}}
	queries := 0
	selectionFailure := errors.New("selected record denied")
	service = NewSurfaceContextApplicationService(SurfaceContextDependencies{
		Objects: func() map[string]definitionmodel.ObjectSchema { return objects },
		ListRecords: func(_ context.Context, objectKey string, query recordmodel.RecordListQuery, _ principalmodel.Principal) (recordmodel.RecordPageResult, error) {
			queries++
			if objectKey == "denied" {
				return recordmodel.RecordPageResult{}, &apperror.AppError{Kind: apperror.KindForbidden, Code: "denied"}
			}
			if objectKey == "customer" && (query.Page != 1 || query.PageSize != maxPageSize || query.Search != "needle") {
				t.Fatalf("query=%#v", query)
			}
			return recordmodel.RecordPageResult{Items: []recordmodel.Record{}, Page: query.Page, PageSize: query.PageSize}, nil
		},
		GetRecord: func(context.Context, string, string, principalmodel.Principal) (recordmodel.Record, error) {
			return recordmodel.Record{}, selectionFailure
		},
	})
	items := []surfacecontextmodel.SurfaceContextObjectRequest{{ObjectKey: " "}, {ObjectKey: "customer", Page: -1, PageSize: 500, Search: " needle "}, {ObjectKey: "customer"}, {ObjectKey: "unknown"}, {ObjectKey: "denied"}}
	for index := len(items); index < maxObjects+3; index++ {
		items = append(items, surfacecontextmodel.SurfaceContextObjectRequest{ObjectKey: fmt.Sprintf("object-%02d", index)})
	}
	principal := accessfixture.Attach(principalmodel.Principal{}, accessfixture.Bundle{FieldPolicies: []accessfixture.FieldPolicyFixture{{ObjectKey: "customer", FieldKey: "secret", Read: true, Masked: true}}})
	result = service.Context(t.Context(), surfacecontextmodel.SurfaceContextRequest{SelectedObjectKey: "customer", SelectedRecordID: "c1", Objects: items}, principal)
	if len(result.Objects) > maxObjects || queries != len(result.Objects) || result.Diagnostics.MaskedFieldCount != 1 || result.Diagnostics.DeniedObjectCount != 1 || result.Selected == nil || result.Selected.Error != selectionFailure.Error() {
		t.Fatalf("objects=%d queries=%d diagnostics=%#v selected=%#v", len(result.Objects), queries, result.Diagnostics, result.Selected)
	}
	service.dependencies.GetRecord = func(context.Context, string, string, principalmodel.Principal) (recordmodel.Record, error) {
		return recordmodel.Record{ID: "c1"}, nil
	}
	selected := service.selectedRecord(t.Context(), surfacecontextmodel.SurfaceContextRequest{SelectedObjectKey: " customer ", SelectedRecordID: " c1 "}, principal)
	if selected == nil || selected.Record.ID != "c1" {
		t.Fatalf("selected=%#v", selected)
	}
	if selected := service.selectedRecord(t.Context(), surfacecontextmodel.SurfaceContextRequest{SelectedObjectKey: "customer", SelectedRecordID: " "}, principal); selected != nil {
		t.Fatalf("blank selected record=%#v", selected)
	}
}

func TestSurfaceContextRelationProjectionSkipsUnavailableAndPrivateTargets(t *testing.T) {
	source := definitionmodel.ObjectSchema{Key: "source", Fields: []definitionmodel.FieldSchema{
		{Key: "plain", Type: "text"},
		{Key: "missing_target", Type: "relation"},
		{Key: "target", Type: "relation", Config: map[string]any{"object_key": "target"}},
		{Key: "identity", Type: "relation", Config: map[string]any{"object_key": "identity_user"}},
	}}
	objects := map[string]surfacecontextmodel.SurfaceContextObjectResult{
		"unknown": {Page: recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "u"}}}},
		"source": {
			Page: recordmodel.RecordPageResult{
				Items: []recordmodel.Record{
					{ID: "s0", Data: map[string]any{"target": "", "identity": "other"}},
					{ID: "s1", Data: map[string]any{"target": nil, "identity": "self"}},
					{ID: "s2", Data: map[string]any{"target": "t1", "identity": "self"}},
				},
			},
		},
	}
	directoryFailure := errors.New("directory failed")
	service := NewSurfaceContextApplicationService(SurfaceContextDependencies{
		Objects: func() map[string]definitionmodel.ObjectSchema {
			return map[string]definitionmodel.ObjectSchema{"source": source}
		},
		ListDirectoryUsers: func(context.Context) ([]identitysdk.User, error) { return nil, directoryFailure },
	})
	projections, labels := service.relationProjections(t.Context(), objects, principalmodel.Principal{Principal: identitysdk.Principal{UserID: "self"}})
	if len(projections) != 0 || len(labels) != 0 {
		t.Fatalf("projections=%#v labels=%#v", projections, labels)
	}
	service.dependencies.ListDirectoryUsers = func(context.Context) ([]identitysdk.User, error) {
		return []identitysdk.User{{ID: "unrequested"}, {ID: "other"}, {ID: "self"}}, nil
	}
	projections, _ = service.relationProjections(t.Context(), objects, principalmodel.Principal{Principal: identitysdk.Principal{UserID: "self"}})
	if len(projections) != 2 {
		t.Fatalf("self projections=%#v", projections)
	}
	service.dependencies.ListDirectoryUsers = nil
	projections, _ = service.relationProjections(t.Context(), objects, principalmodel.Principal{Principal: identitysdk.Principal{UserID: "other"}})
	if len(projections) != 0 {
		t.Fatalf("private directory projections=%#v", projections)
	}
}

func TestSurfaceContextLabelOnlyTargetFailureBoundaries(t *testing.T) {
	target := definitionmodel.ObjectSchema{Key: "target", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	request := relationRequest{sourceObjectKey: "source", sourceRecordID: "s1", fieldKey: "target", targetObjectKey: "target", targetRecordID: "t1"}
	ids := map[string]bool{"t1": true}
	service := NewSurfaceContextApplicationService(SurfaceContextDependencies{})
	if got := service.labelOnlyTargets(t.Context(), nil, []relationRequest{request}, "target", ids, principalmodel.Principal{}); len(got) != 0 {
		t.Fatalf("missing schema targets=%#v", got)
	}
	if got := service.labelOnlyTargets(t.Context(), map[string]definitionmodel.ObjectSchema{"target": target}, []relationRequest{request}, "target", ids, principalmodel.Principal{}); len(got) != 0 {
		t.Fatalf("missing storage dependency targets=%#v", got)
	}
	service.dependencies.ListStoredRecords = func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
		return recordmodel.RecordPageResult{}, nil
	}
	if got := service.labelOnlyTargets(t.Context(), map[string]definitionmodel.ObjectSchema{"target": target}, []relationRequest{{targetObjectKey: "other"}, {targetObjectKey: "target", targetRecordID: "missing"}, request}, "target", ids, principalmodel.Principal{}); len(got) != 0 {
		t.Fatalf("unpermitted targets=%#v", got)
	}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace-a"}}, accessfixture.Bundle{ReferencePolicies: []accessfixture.ReferencePolicyFixture{{SourceObjectKey: "source", RelationFieldKey: "target", TargetObjectKey: "target", DisplayFields: []string{"name"}, Mode: "label_only"}}})
	storageFailure := errors.New("stored target failed")
	service.dependencies.ListStoredRecords = func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
		return recordmodel.RecordPageResult{}, storageFailure
	}
	if got := service.labelOnlyTargets(t.Context(), map[string]definitionmodel.ObjectSchema{"target": target}, []relationRequest{request}, "target", ids, principal); len(got) != 0 {
		t.Fatalf("failed stored targets=%#v", got)
	}
}

func TestSurfaceContextReportsRecognizeSchemaAndConventionalObjects(t *testing.T) {
	service := NewSurfaceContextApplicationService(SurfaceContextDependencies{Reports: func() []reportmodel.ReportSchema {
		return []reportmodel.ReportSchema{{Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "customer", Alias: "customer"}}}}
	}})
	objects := map[string]surfacecontextmodel.SurfaceContextObjectResult{
		"customer":       {Page: recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "1"}}, Total: 2}},
		"file_download":  {},
		"report_history": {Page: recordmodel.RecordPageResult{Total: 3}},
		"sales_export":   {},
		"ordinary":       {},
	}
	reports := service.reports(objects)
	if len(reports) != 4 || reports[0]["object_key"] != "customer" || reports[1]["object_key"] != "file_download" || reports[2]["object_key"] != "report_history" || reports[3]["object_key"] != "sales_export" {
		t.Fatalf("reports=%#v", reports)
	}
	if objects := NewSurfaceContextApplicationService(SurfaceContextDependencies{}).objects(); len(objects) != 0 {
		t.Fatalf("objects=%#v", objects)
	}
}
