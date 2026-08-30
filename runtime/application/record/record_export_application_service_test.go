// Export application service tests.
package record

import (
	identitysdk "github.com/domainry/domainry-identity-sdk"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"context"
	"encoding/csv"
	"errors"
	"strings"
	"testing"

	dataexchange "github.com/domainry/domainry-data-exchange/fileengine"
	"github.com/domainry/domainry-foundation/apperror"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type exportRepositoryProbe struct {
	recordrepository.RecordRepository
	page      recordmodel.RecordPageResult
	lastQuery recordmodel.RecordListQuery
}

func (r *exportRepositoryProbe) ListRecords(_ context.Context, _ string, _ definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	r.lastQuery = query
	return r.page, nil
}

func TestExportServiceOwnsCSVRelationDisplayAndAudit(t *testing.T) {
	order := definitionmodel.ObjectSchema{Key: "order", Fields: []definitionmodel.FieldSchema{
		{Key: "number", Type: "text"},
		{Key: "customer_id", Type: "relation", Config: map[string]any{"object_key": "customer"}},
	}}
	customer := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	objects := map[string]definitionmodel.ObjectSchema{"order": order, "customer": customer}
	repository := &exportRepositoryProbe{page: recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "order-1", CreatedAt: "created", UpdatedAt: "updated", Data: map[string]any{"number": "SO-1", "customer_id": "customer-1"}}}}}
	relationCalls := 0
	var auditMetadata map[string]any
	service := NewRecordExportApplicationService(RecordExportDependencies{
		Repository:           repository,
		Objects:              func() map[string]definitionmodel.ObjectSchema { return objects },
		EnsureSnapshotAccess: func(definitionmodel.ObjectSchema, string, principalmodel.Principal) error { return nil },
		NormalizeQuery: func(_ definitionmodel.ObjectSchema, query recordmodel.RecordListQuery, _ principalmodel.Principal) recordmodel.RecordListQuery {
			query.Search = "filtered"
			return query
		},
		CanAccess: func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return true },
		ListRecords: func(_ context.Context, objectKey string, query recordmodel.RecordListQuery, _ principalmodel.Principal) (recordmodel.RecordPageResult, error) {
			relationCalls++
			if objectKey != "customer" || query.PageSize != 1 {
				t.Fatalf("relation query object=%q query=%#v", objectKey, query)
			}
			return recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "customer-1", Data: map[string]any{"name": "Acme"}}}}, nil
		},
		RecordDisplay: func(_ definitionmodel.ObjectSchema, record recordmodel.Record) (string, string) {
			return record.ID, record.Data["name"].(string)
		},
		Audit: func(_ context.Context, event, _, _ string, _ principalmodel.Principal, _ string, _, _ map[string]any, metadata map[string]any) {
			if event == "record_exported" {
				auditMetadata = metadata
			}
		},
	})
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "admin"}}, accessfixture.Bundle{Key: "admin", Permissions: []string{"workspace.admin", "*"}, RecordScope: "all_records"})

	content, filename, err := service.ExportWithOptions(t.Context(), "order", principal, RecordExportOptions{Reason: "analysis", Query: recordmodel.RecordListQuery{Search: "original", Locale: "zh-CN", FallbackLocale: "en-US"}})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(string(content))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if filename != "order.csv" || len(rows) != 2 || len(rows[0]) != 6 || rows[0][5] != "customer_id__display" || rows[1][4] != "customer-1" || rows[1][5] != "Acme" {
		t.Fatalf("filename=%q rows=%#v", filename, rows)
	}
	if repository.lastQuery.Page != 1 || repository.lastQuery.PageSize != 200 || repository.lastQuery.Search != "filtered" || repository.lastQuery.Locale != "zh-CN" || repository.lastQuery.FallbackLocale != "en-US" || relationCalls != 1 {
		t.Fatalf("query=%#v relationCalls=%d", repository.lastQuery, relationCalls)
	}
	if auditMetadata["record_count"] != 1 || auditMetadata["export_reason"] != "analysis" || auditMetadata["download_status"] != "ready" {
		t.Fatalf("audit metadata = %#v", auditMetadata)
	}
}

func TestExportServiceBindsAssuranceToCanonicalIntentAndAuditEvidence(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}, ExportAssurancePolicy: &definitionmodel.ActionAssurancePolicy{RequiredMethods: []string{definitionmodel.ActionAssuranceOTP}}}
	repository := &exportRepositoryProbe{page: recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "customer-1", Data: map[string]any{"name": "Acme"}}}}}
	events := map[string]map[string]any{}
	service := NewRecordExportApplicationService(RecordExportDependencies{
		Repository: repository, Objects: func() map[string]definitionmodel.ObjectSchema {
			return map[string]definitionmodel.ObjectSchema{"customer": object}
		},
		ValidateAssurance: func(_ context.Context, got definitionmodel.ObjectSchema, _ principalmodel.Principal, intent map[string]any, token string) (map[string]string, error) {
			if got.Key != object.Key || token != "verified-token" || intent["reason"] != "month close" || intent["filter_summary"] != "active customers" {
				t.Fatalf("object=%s token=%q intent=%#v", got.Key, token, intent)
			}
			query, ok := intent["query"].(recordmodel.RecordListQuery)
			if !ok || query.Search != "Acme" {
				t.Fatalf("query intent=%#v", intent["query"])
			}
			return map[string]string{"grant_id": "grant-1", "methods": "otp", "payload_digest": "digest-1"}, nil
		},
		Audit: func(_ context.Context, event, _, _ string, _ principalmodel.Principal, _ string, _, _ map[string]any, metadata map[string]any) {
			events[event] = metadata
		},
	})
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "operator"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin", "*"}})
	options := RecordExportOptions{Fields: []string{"name"}, Reason: " month close ", FilterSummary: " active customers ", MaskingPolicy: "strict", Query: recordmodel.RecordListQuery{Search: "Acme"}, AssuranceToken: "verified-token"}
	content, _, err := service.ExportWithOptions(t.Context(), object.Key, principal, options)
	if err != nil {
		t.Fatal(err)
	}
	if events["record_export_assurance_succeeded"]["grant_id"] != "grant-1" || events["record_exported"]["assurance_payload_digest"] != "digest-1" || events["record_exported"]["download_sha256"] == "" || events["record_exported"]["download_bytes"] != len(content) {
		t.Fatalf("events=%#v", events)
	}

	unavailable := NewRecordExportApplicationService(RecordExportDependencies{Repository: repository, Objects: func() map[string]definitionmodel.ObjectSchema {
		return map[string]definitionmodel.ObjectSchema{"customer": object}
	}})
	if _, _, err := unavailable.ExportWithOptions(t.Context(), object.Key, principal, options); apperror.CodeOf(err) != "backend.export.assurance_unavailable" {
		t.Fatalf("fail-closed error=%v", err)
	}
}

func TestExportServiceKeepsCurrencyCanonicalAndRejectsBinaryFloat(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "invoice", Fields: []definitionmodel.FieldSchema{{Key: "amount", Type: "currency", Config: map[string]any{"precision": 12, "scale": 2, "currency_code": "USD"}}}}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}, accessfixture.Bundle{
		Permissions: []string{"invoice.export"}, DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: "invoice", Scope: "all_records", Read: true}},
	})
	repository := &exportRepositoryProbe{page: recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "invoice-1", Data: map[string]any{"amount": "0.3"}}}}}
	service := NewRecordExportApplicationService(RecordExportDependencies{Repository: repository, Objects: func() map[string]definitionmodel.ObjectSchema {
		return map[string]definitionmodel.ObjectSchema{"invoice": object}
	}})
	content, _, err := service.Export(t.Context(), "invoice", principal)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(string(content))).ReadAll()
	if err != nil || len(rows) != 2 || rows[1][3] != "0.30" {
		t.Fatalf("rows=%#v err=%v", rows, err)
	}
	repository.page.Items[0].Data["amount"] = float64(0.3)
	if _, _, err := service.Export(t.Context(), "invoice", principal); apperror.CodeOf(err) != "backend.export.decimal_value_invalid" {
		t.Fatalf("binary float export error=%v", err)
	}
}

func TestExportServiceResolvesIdentityLabelsWithinPrincipalScope(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "task", Fields: []definitionmodel.FieldSchema{{Key: "assignee", Type: "relation", Config: map[string]any{"object_key": "identity_user"}}}}
	repository := &exportRepositoryProbe{page: recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "task-1", Data: map[string]any{"assignee": "u1"}}}}}
	service := NewRecordExportApplicationService(RecordExportDependencies{
		Repository: repository,
		Objects: func() map[string]definitionmodel.ObjectSchema {
			return map[string]definitionmodel.ObjectSchema{"task": object}
		},
		CanAccess: func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return true },
		ListDirectoryUsers: func(context.Context) ([]identitysdk.User, error) {
			return []identitysdk.User{{ID: "u1", Name: "Alice"}, {ID: "u2", Name: "Bob"}}, nil
		},
	})
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "u1"}}, accessfixture.Bundle{Permissions: []string{"task.export"}, DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: "task", Scope: "owned_records", Read: true}}})

	content, _, err := service.Export(t.Context(), "task", principal)
	if err != nil {
		t.Fatal(err)
	}
	rows, _ := csv.NewReader(strings.NewReader(string(content))).ReadAll()
	if len(rows) != 2 || rows[1][3] != "u1" || rows[1][4] != "Alice" {
		t.Fatalf("identity export = %#v", rows)
	}
}

func TestExportServiceAuditsUnknownPrincipalDenial(t *testing.T) {
	var event string
	service := NewRecordExportApplicationService(RecordExportDependencies{
		Objects: func() map[string]definitionmodel.ObjectSchema {
			return map[string]definitionmodel.ObjectSchema{"customer": {Key: "customer"}}
		},
		Audit: func(_ context.Context, value, _, _ string, _ principalmodel.Principal, _ string, _, _, _ map[string]any) {
			event = value
		},
	})
	_, _, err := service.Export(t.Context(), "customer", principalmodel.Principal{})
	assertRecordApplicationError(t, err, apperror.KindForbidden, "backend.workspace_scope_required", nil)
	if event != "" {
		t.Fatalf("audit was called before workspace authorization: %q", event)
	}
}

func TestExportServiceBoundsOutputAndHonorsCancellation(t *testing.T) {
	var output strings.Builder
	buffer := dataexchange.BoundedWriter{Writer: &output, Limit: 3}
	if _, err := buffer.Write([]byte("four")); !errors.Is(err, dataexchange.ErrPayloadTooLarge) || output.Len() != 0 {
		t.Fatalf("bounded buffer accepted oversized write: bytes=%q err=%v", output.String(), err)
	}
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	service := NewRecordExportApplicationService(RecordExportDependencies{
		Repository: &exportRepositoryProbe{},
		Objects: func() map[string]definitionmodel.ObjectSchema {
			return map[string]definitionmodel.ObjectSchema{"customer": object}
		},
	})
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}, accessfixture.Bundle{
		Permissions: []string{"customer.export"}, DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: "customer", Scope: "all_records", Read: true}},
	})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := service.Export(ctx, "customer", principal); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error=%v", err)
	}
}
