package record

import (
	"context"
	"errors"
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
)

type exportEdgeRepository struct {
	recordrepository.RecordRepository
	list func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error)
}

func (r *exportEdgeRepository) ListRecords(ctx context.Context, workspaceID string, object definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	return r.list(ctx, workspaceID, object, query)
}

func recordExportEdgeService(object definitionmodel.ObjectSchema, repository recordrepository.RecordRepository) *RecordExportApplicationService {
	return NewRecordExportApplicationService(RecordExportDependencies{
		Repository: repository,
		Objects: func() map[string]definitionmodel.ObjectSchema {
			return map[string]definitionmodel.ObjectSchema{object.Key: object}
		},
	})
}

func recordExportPrincipal(objectKey string) principalmodel.Principal {
	return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}, accessfixture.Bundle{
		Permissions: []string{objectKey + ".export"},
		DataPolicies: []accessfixture.DataPolicyFixture{{
			ObjectKey: objectKey, Scope: "all", Read: true,
		}},
	})
}

func exportRecordDirectForTest(ctx context.Context, service *RecordExportApplicationService, objectKey string, principal principalmodel.Principal, options RecordExportOptions) ([]byte, string, error) {
	object, fields, evidence, err := service.prepareExport(ctx, objectKey, principal, options, nil, false)
	if err != nil {
		return nil, "", err
	}
	return service.exportPrepared(ctx, recordExportPrepared{object: object, fields: fields, evidence: evidence, options: options, principal: principal}, recordExportDirectMaxRows)
}

func TestExportAuthorizationAndFieldSelectionFailures(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	emptyRepository := &exportEdgeRepository{list: func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
		return recordmodel.RecordPageResult{}, nil
	}}
	for _, test := range []struct {
		name      string
		service   *RecordExportApplicationService
		principal principalmodel.Principal
		options   RecordExportOptions
		wantCode  string
	}{
		{name: "object missing", service: NewRecordExportApplicationService(RecordExportDependencies{}), principal: recordExportPrincipal(object.Key), wantCode: "backend.object.not_found"},
		{name: "permission denied", service: recordExportEdgeService(object, emptyRepository), principal: principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}, wantCode: "backend.permission.denied"},
		{name: "missing SDK access bundle", service: recordExportEdgeService(object, emptyRepository), principal: accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}, accessfixture.Bundle{Permissions: []string{"customer.export"}, DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: "customer"}}}), wantCode: "backend.permission.denied"},
		{name: "no object fields", service: recordExportEdgeService(definitionmodel.ObjectSchema{Key: "customer"}, emptyRepository), principal: recordExportPrincipal(object.Key), wantCode: "backend.export.no_fields"},
		{name: "requested fields excluded", service: recordExportEdgeService(object, emptyRepository), principal: recordExportPrincipal(object.Key), options: RecordExportOptions{Fields: []string{"missing"}}, wantCode: "backend.export.no_fields"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := exportRecordDirectForTest(t.Context(), test.service, object.Key, test.principal, test.options)
			if apperror.CodeOf(err) != test.wantCode {
				t.Fatalf("err=%v code=%q want=%q", err, apperror.CodeOf(err), test.wantCode)
			}
		})
	}
}

func TestExportSnapshotRepositoryAndCapacityFailures(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	principal := recordExportPrincipal(object.Key)
	failure := errors.New("export dependency failed")

	snapshot := recordExportEdgeService(object, &exportEdgeRepository{list: func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
		return recordmodel.RecordPageResult{}, nil
	}})
	snapshot.dependencies.EnsureSnapshotAccess = func(definitionmodel.ObjectSchema, string, principalmodel.Principal) error { return failure }
	if _, _, err := exportRecordDirectForTest(t.Context(), snapshot, object.Key, principal, RecordExportOptions{}); !errors.Is(err, failure) {
		t.Fatalf("snapshot err=%v", err)
	}

	repositoryFailure := recordExportEdgeService(object, &exportEdgeRepository{list: func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
		return recordmodel.RecordPageResult{}, failure
	}})
	if _, _, err := exportRecordDirectForTest(t.Context(), repositoryFailure, object.Key, principal, RecordExportOptions{}); apperror.CodeOf(err) != "backend.internal" || !errors.Is(err, failure) {
		t.Fatalf("repository err=%v", err)
	}

	records := make([]recordmodel.Record, recordExportDirectMaxRows+1)
	for index := range records {
		records[index] = recordmodel.Record{ID: "record", Data: map[string]any{"name": "value"}}
	}
	tooMany := recordExportEdgeService(object, &exportEdgeRepository{list: func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
		return recordmodel.RecordPageResult{Items: records}, nil
	}})
	if _, _, err := exportRecordDirectForTest(t.Context(), tooMany, object.Key, principal, RecordExportOptions{}); apperror.CodeOf(err) != "backend.export.too_many_records" {
		t.Fatalf("too many err=%v code=%q", err, apperror.CodeOf(err))
	}

	tooLarge := recordExportEdgeService(object, &exportEdgeRepository{list: func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
		return recordmodel.RecordPageResult{Items: []recordmodel.Record{
			{ID: "record-1", Data: map[string]any{"name": strings.Repeat("x", recordExportMaxBytes)}},
		}}, nil
	}})
	if _, _, err := exportRecordDirectForTest(t.Context(), tooLarge, object.Key, principal, RecordExportOptions{}); apperror.CodeOf(err) != "backend.export.output_too_large" {
		t.Fatalf("too large err=%v code=%q", err, apperror.CodeOf(err))
	}
}

func TestExportPaginationAccessFilteringAndMidPageCancellation(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	principal := recordExportPrincipal(object.Key)
	pages := 0
	repository := &exportEdgeRepository{list: func(_ context.Context, _ string, _ definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
		pages++
		if query.Page != 1 || query.PageSize != recordExportBatchSize || !query.SkipTotal || len(query.Sort) != 1 || query.Sort[0].Field != "id" || query.Sort[0].Direction != "asc" {
			t.Fatalf("export did not use stable keyset pagination: %#v", query)
		}
		if query.AfterID == "" {
			return recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "hidden", Data: map[string]any{"name": "Hidden"}}}, HasNext: true}, nil
		}
		if query.AfterID != "hidden" {
			t.Fatalf("second page cursor=%q", query.AfterID)
		}
		return recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "visible", Data: map[string]any{"name": "Visible"}}}}, nil
	}}
	service := recordExportEdgeService(object, repository)
	service.dependencies.ProjectRecords = func(_ context.Context, _ principalmodel.Principal, _ definitionmodel.ObjectSchema, records []recordmodel.Record, _ string) ([]recordmodel.Record, error) {
		visible := make([]recordmodel.Record, 0, len(records))
		for _, record := range records {
			if record.ID != "hidden" {
				visible = append(visible, record)
			}
		}
		return visible, nil
	}
	content, _, err := exportRecordDirectForTest(t.Context(), service, object.Key, principal, RecordExportOptions{})
	if err != nil || pages != 2 || strings.Contains(string(content), "Hidden") || !strings.Contains(string(content), "Visible") {
		t.Fatalf("pages=%d content=%q err=%v", pages, content, err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancelRepository := &exportEdgeRepository{list: func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
		return recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "first", Data: map[string]any{"name": "First"}}, {ID: "second", Data: map[string]any{"name": "Second"}}}}, nil
	}}
	cancelService := recordExportEdgeService(object, cancelRepository)
	cancelService.dependencies.ProjectRecords = func(context.Context, principalmodel.Principal, definitionmodel.ObjectSchema, []recordmodel.Record, string) ([]recordmodel.Record, error) {
		cancel()
		return nil, context.Canceled
	}
	if _, _, err := exportRecordDirectForTest(ctx, cancelService, object.Key, principal, RecordExportOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("mid-page cancellation err=%v", err)
	}
}

func TestExportRelationLabelFallbackEdges(t *testing.T) {
	source := definitionmodel.ObjectSchema{Key: "order"}
	maskedField := definitionmodel.FieldSchema{Key: "secret_customer", Type: "relation", Config: map[string]any{"object_key": "customer"}}
	missingTarget := definitionmodel.FieldSchema{Key: "untyped_relation", Type: "relation"}
	principal := accessfixture.Attach(principalmodel.Principal{}, accessfixture.Bundle{FieldPolicies: []accessfixture.FieldPolicyFixture{{ObjectKey: source.Key, FieldKey: maskedField.Key, Export: true, Masked: true}}})
	service := NewRecordExportApplicationService(RecordExportDependencies{})
	labels := service.relationLabels(t.Context(), source, []definitionmodel.FieldSchema{maskedField, missingTarget}, []recordmodel.Record{{Data: map[string]any{maskedField.Key: "customer-1"}}}, principal)
	if len(labels) != 0 {
		t.Fatalf("masked/missing-target labels=%#v", labels)
	}

	field := definitionmodel.FieldSchema{Key: "customer_id", Type: "relation", Config: map[string]any{"object_key": "customer"}}
	records := []recordmodel.Record{{Data: map[string]any{field.Key: "customer-1"}}}
	labels = service.relationLabels(t.Context(), source, []definitionmodel.FieldSchema{field}, records, principalmodel.Principal{})
	if len(labels[field.Key]) != 0 {
		t.Fatalf("labels without resolver=%#v", labels)
	}
	secondField := definitionmodel.FieldSchema{Key: "billing_customer_id", Type: "relation", Config: map[string]any{"object_key": "customer"}}
	labels = service.relationLabels(t.Context(), source, []definitionmodel.FieldSchema{field, secondField}, []recordmodel.Record{
		{Data: map[string]any{field.Key: "", secondField.Key: nil}},
		{Data: map[string]any{field.Key: "customer-1", secondField.Key: "customer-2"}},
	}, principalmodel.Principal{})
	if len(labels) != 2 {
		t.Fatalf("shared target labels=%#v", labels)
	}

	service.dependencies.ListRecords = func(context.Context, string, recordmodel.RecordListQuery, principalmodel.Principal) (recordmodel.RecordPageResult, error) {
		return recordmodel.RecordPageResult{}, errors.New("resolver unavailable")
	}
	labels = service.relationLabels(t.Context(), source, []definitionmodel.FieldSchema{field}, records, principalmodel.Principal{})
	if len(labels[field.Key]) != 0 {
		t.Fatalf("labels after resolver failure=%#v", labels)
	}

	service.dependencies.Objects = func() map[string]definitionmodel.ObjectSchema {
		return map[string]definitionmodel.ObjectSchema{"customer": {Key: "customer"}}
	}
	service.dependencies.ListRecords = func(_ context.Context, _ string, query recordmodel.RecordListQuery, _ principalmodel.Principal) (recordmodel.RecordPageResult, error) {
		if query.PageSize != 1 {
			t.Fatalf("query=%#v", query)
		}
		return recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "customer-1"}}}, nil
	}
	labels = service.relationLabels(t.Context(), source, []definitionmodel.FieldSchema{field}, records, principalmodel.Principal{})
	if labels[field.Key]["customer-1"] != "customer-1" {
		t.Fatalf("fallback labels=%#v", labels)
	}
}

func TestExportRelationLabelCapsLookupAndIdentityProjectionFallbacks(t *testing.T) {
	field := definitionmodel.FieldSchema{Key: "customer_id", Type: "relation", Config: map[string]any{"object_key": "customer"}}
	records := make([]recordmodel.Record, 201)
	for index := range records {
		records[index] = recordmodel.Record{Data: map[string]any{field.Key: "customer-" + strings.Repeat("x", index+1)}}
	}
	service := NewRecordExportApplicationService(RecordExportDependencies{
		ListRecords: func(_ context.Context, _ string, query recordmodel.RecordListQuery, _ principalmodel.Principal) (recordmodel.RecordPageResult, error) {
			if query.PageSize != 200 {
				t.Fatalf("page size=%d", query.PageSize)
			}
			return recordmodel.RecordPageResult{}, nil
		},
	})
	service.relationLabels(t.Context(), definitionmodel.ObjectSchema{Key: "order"}, []definitionmodel.FieldSchema{field}, records, principalmodel.Principal{})

	labels := map[string]string{}
	service.identityLabels(t.Context(), map[string]bool{"user-2": true}, principalmodel.Principal{Principal: identitysdk.Principal{UserID: "user-1"}}, labels)
	if len(labels) != 0 {
		t.Fatalf("unauthorized labels=%#v", labels)
	}
	service.dependencies.ListIdentityUsers = func(context.Context) ([]identitysdk.User, error) {
		return nil, errors.New("projection unavailable")
	}
	service.identityLabels(t.Context(), map[string]bool{"user-2": true}, principalmodel.Principal{Principal: identitysdk.Principal{UserID: "user-1"}}, labels)
	service.identityLabels(t.Context(), map[string]bool{"user-1": true}, principalmodel.Principal{Principal: identitysdk.Principal{UserID: "user-1"}}, labels)
	if len(labels) != 0 {
		t.Fatalf("failed projection labels=%#v", labels)
	}
	service.dependencies.ListIdentityUsers = func(context.Context) ([]identitysdk.User, error) {
		return []identitysdk.User{{ID: "user-1", Name: " "}, {ID: "user-2", Name: "Other"}}, nil
	}
	service.identityLabels(t.Context(), map[string]bool{"user-1": true, "user-2": true}, principalmodel.Principal{Principal: identitysdk.Principal{UserID: "user-1"}}, labels)
	if labels["user-1"] != "user-1" || labels["user-2"] != "" {
		t.Fatalf("self labels=%#v", labels)
	}
	siblingLabels := map[string]string{}
	siblingReader := accessfixture.Attach(principalmodel.Principal{}, recordFullAccessBundle("identity.users.get"))
	service.identityLabels(t.Context(), map[string]bool{"user-2": true}, siblingReader, siblingLabels)
	if len(siblingLabels) != 0 {
		t.Fatalf("sibling Action resolved projection labels=%#v", siblingLabels)
	}
	adminLabels := map[string]string{}
	directoryReader := accessfixture.Attach(principalmodel.Principal{}, recordFullAccessBundle(identityUsersListAction))
	service.identityLabels(t.Context(), map[string]bool{"user-2": true}, directoryReader, adminLabels)
	if adminLabels["user-2"] != "Other" {
		t.Fatalf("admin labels=%#v", adminLabels)
	}
}

func TestExportErrorIgnoresBlankParameterKey(t *testing.T) {
	err := recordExportError(apperror.KindBadRequest, "backend.export.too_many_records", nil, " ", "ignored")
	if apperror.ParamsOf(err) != nil {
		t.Fatalf("err=%#v", err)
	}
}
