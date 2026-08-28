// Surface-context domain service tests.
package surfacecontext

import (
	identitysdk "github.com/domainry/domainry-identity-sdk"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	surfacecontextmodel "github.com/domainry/domainry-runtime/runtime/domain/surfacecontext/model"

	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestContextWithoutQueriesReturnsBusinessContextOnly(t *testing.T) {
	service := NewSurfaceContextApplicationService(SurfaceContextDependencies{Objects: func() map[string]definitionmodel.ObjectSchema { return map[string]definitionmodel.ObjectSchema{} }})
	result := service.Context(t.Context(), surfacecontextmodel.SurfaceContextRequest{SurfaceKey: "dashboard", Include: []string{"metrics"}}, principalmodel.Principal{Principal: identitysdk.Principal{Known: true}})
	if result.SurfaceKey != "dashboard" || len(result.Objects) != 0 || len(result.Diagnostics.Include) != 1 {
		t.Fatalf("result=%#v", result)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{`"layout"`, `"components"`, `"style"`, `"ui_blueprint"`} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("surface context leaked frontend field %s: %s", forbidden, raw)
		}
	}
}

func TestContextProjectsIdentityRelationsOnlyWithDirectoryPermission(t *testing.T) {
	profile := definitionmodel.ObjectSchema{Key: "employee_profile", Fields: []definitionmodel.FieldSchema{{Key: "identity_user", Type: "relation", Config: map[string]any{"object_key": "identity_user"}}}}
	listCalls, directoryCalls := 0, 0
	service := NewSurfaceContextApplicationService(SurfaceContextDependencies{
		Objects: func() map[string]definitionmodel.ObjectSchema {
			return map[string]definitionmodel.ObjectSchema{"employee_profile": profile}
		},
		ListRecords: func(_ context.Context, objectKey string, query recordmodel.RecordListQuery, _ principalmodel.Principal) (recordmodel.RecordPageResult, error) {
			listCalls++
			return recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "p1", Data: map[string]any{"identity_user": "u1"}}, {ID: "p2", Data: map[string]any{"identity_user": "u2"}}}, Page: query.Page, PageSize: query.PageSize}, nil
		},
		ListDirectoryUsers: func(context.Context) ([]identitysdk.User, error) {
			directoryCalls++
			return []identitysdk.User{{ID: "u1", Name: "Ada"}, {ID: "u2", Name: "Lin"}}, nil
		},
	})
	admin := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "admin"}}, accessfixture.Bundle{Permissions: []string{"identity.users.read"}})
	result := service.Context(t.Context(), surfacecontextmodel.SurfaceContextRequest{Objects: []surfacecontextmodel.SurfaceContextObjectRequest{{ObjectKey: "employee_profile"}}}, admin)
	if len(result.RelationProjections) != 2 || result.RelationProjections[0].DisplayField != "name" || result.RelationProjections[0].DetailRoute != "/security/accounts/u1" || listCalls != 1 || directoryCalls != 1 {
		t.Fatalf("identity projections=%#v list=%d directory=%d", result.RelationProjections, listCalls, directoryCalls)
	}
	limited := service.Context(t.Context(), surfacecontextmodel.SurfaceContextRequest{Objects: []surfacecontextmodel.SurfaceContextObjectRequest{{ObjectKey: "employee_profile"}}}, principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "reader"}})
	if len(limited.RelationProjections) != 0 || directoryCalls != 1 {
		t.Fatalf("directory relation leaked: %#v calls=%d", limited.RelationProjections, directoryCalls)
	}
}

func TestContextUsesLabelOnlyReferencePermissionWithoutTargetRead(t *testing.T) {
	profile := definitionmodel.ObjectSchema{Key: "employee_profile", Fields: []definitionmodel.FieldSchema{{Key: "position", Type: "relation", Config: map[string]any{"object_key": "hr_position"}}}}
	position := definitionmodel.ObjectSchema{Key: "hr_position", Fields: []definitionmodel.FieldSchema{{Key: "title", Type: "text"}, {Key: "grade", Type: "text"}}}
	service := NewSurfaceContextApplicationService(SurfaceContextDependencies{
		Objects: func() map[string]definitionmodel.ObjectSchema {
			return map[string]definitionmodel.ObjectSchema{"employee_profile": profile, "hr_position": position}
		},
		ListRecords: func(_ context.Context, objectKey string, query recordmodel.RecordListQuery, _ principalmodel.Principal) (recordmodel.RecordPageResult, error) {
			if objectKey == "employee_profile" {
				return recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "p1", Data: map[string]any{"position": "pos-1"}}}, Page: query.Page, PageSize: query.PageSize}, nil
			}
			return recordmodel.RecordPageResult{}, errors.New("target read denied")
		},
		ListStoredRecords: func(_ context.Context, _ string, _ definitionmodel.ObjectSchema, _ recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
			return recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "pos-1", Data: map[string]any{"title": "Sales Representative", "grade": "P4"}}}}, nil
		},
	})
	role := accessfixture.Bundle{ReferencePolicies: []accessfixture.ReferencePolicyFixture{{SourceObjectKey: "employee_profile", RelationFieldKey: "position", TargetObjectKey: "hr_position", DisplayFields: []string{"title"}, Mode: "label_only"}}}
	result := service.Context(t.Context(), surfacecontextmodel.SurfaceContextRequest{Objects: []surfacecontextmodel.SurfaceContextObjectRequest{{ObjectKey: "employee_profile"}}}, accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, role))
	if len(result.RelationProjections) != 1 || result.RelationProjections[0].Mode != "label_only" || result.RelationProjections[0].DisplayValue != "Sales Representative" || result.RelationProjections[0].DetailRoute != "" {
		t.Fatalf("label-only projection=%#v", result.RelationProjections)
	}
}

func TestContextBuildsReadableRelationProjection(t *testing.T) {
	order := definitionmodel.ObjectSchema{Key: "order", Fields: []definitionmodel.FieldSchema{{Key: "customer_id", Type: "relation", Config: map[string]any{"object_key": "customer"}}}}
	customer := definitionmodel.ObjectSchema{Key: "customer", UX: map[string]any{"display": map[string]any{"title_field": "customer_no"}}}
	service := NewSurfaceContextApplicationService(SurfaceContextDependencies{
		Objects: func() map[string]definitionmodel.ObjectSchema {
			return map[string]definitionmodel.ObjectSchema{"order": order, "customer": customer}
		},
		ListRecords: func(_ context.Context, objectKey string, query recordmodel.RecordListQuery, _ principalmodel.Principal) (recordmodel.RecordPageResult, error) {
			if objectKey == "order" {
				return recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "o1", Data: map[string]any{"customer_id": "c1"}}}, Page: query.Page, PageSize: query.PageSize}, nil
			}
			return recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "c1", Data: map[string]any{"customer_no": "C-001"}}}, Page: query.Page, PageSize: query.PageSize}, nil
		},
	})
	result := service.Context(t.Context(), surfacecontextmodel.SurfaceContextRequest{Objects: []surfacecontextmodel.SurfaceContextObjectRequest{{ObjectKey: "order", Page: 3, PageSize: 20}}}, principalmodel.Principal{Principal: identitysdk.Principal{Known: true}})
	if result.Objects["order"].Page.Page != 3 || len(result.RelationProjections) != 1 {
		t.Fatalf("result=%#v", result)
	}
	projection := result.RelationProjections[0]
	if projection.DisplayField != "customer_no" || projection.DisplayValue != "C-001" || projection.DetailRoute == "" {
		t.Fatalf("projection=%#v", projection)
	}
}
