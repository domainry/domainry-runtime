package upload

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

type uploadAccessCatalogStub map[string]definitionmodel.ObjectSchema

func (s uploadAccessCatalogStub) ObjectMap(context.Context) map[string]definitionmodel.ObjectSchema {
	return s
}

type workspaceGuardUploadCatalog struct{ calls int }

func (catalog *workspaceGuardUploadCatalog) ObjectMap(context.Context) map[string]definitionmodel.ObjectSchema {
	catalog.calls++
	return nil
}

type uploadAccessAuditStub struct {
	events []string
	reason string
}

func (s *uploadAccessAuditStub) AppendWithMetadata(_ context.Context, event, _, _ string, _ principalmodel.Principal, _ string, _, _, metadata map[string]any) {
	s.events = append(s.events, event)
	s.reason, _ = metadata["reason"].(string)
}

type uploadAccessRecordStub struct {
	record recordmodel.Record
	err    error
}

func (s *uploadAccessRecordStub) GetRecord(context.Context, string, string, principalmodel.Principal) (recordmodel.Record, error) {
	return s.record, s.err
}

func uploadAccessCatalog() uploadAccessCatalogStub {
	return uploadAccessCatalogStub{
		"asset":    {Key: "asset", Fields: []definitionmodel.FieldSchema{{Key: "file_url"}}},
		"document": {Key: "document", Fields: []definitionmodel.FieldSchema{{Key: "file_url"}, {Key: "sensitive"}}},
	}
}

func uploadAccessPrincipal(permissions ...string) principalmodel.Principal {
	grants := make([]identitysdk.FunctionGrant, 0, len(permissions))
	dataPolicies := make([]identitysdk.DataPolicy, 0, len(permissions))
	grantKeys := map[string]bool{}
	addGrant := func(resource, action string) {
		key := resource + "\x00" + action
		if grantKeys[key] {
			return
		}
		grantKeys[key] = true
		grants = append(grants, identitysdk.FunctionGrant{Resource: identitysdk.ResourceType(resource), Action: identitysdk.Action(action), Effect: identitysdk.EffectAllow})
		if resource == "asset" || resource == "document" {
			dataAction := identitysdk.DataActionRead
			if action == "create" || action == "update" || action == "delete" {
				dataAction = identitysdk.DataActionWrite
			}
			dataPolicies = append(dataPolicies, identitysdk.DataPolicy{
				Key: "upload-test-" + resource + "-" + action, Resource: identitysdk.ResourceType(resource), Action: dataAction, Effect: identitysdk.EffectAllow,
				Predicate: identitysdk.Predicate{Fact: "id", Operator: identitysdk.OperatorExists, Value: true},
			})
		}
	}
	for _, permission := range permissions {
		permission = strings.TrimSpace(permission)
		separator := strings.LastIndex(permission, ".")
		if separator <= 0 || separator == len(permission)-1 {
			continue
		}
		resource, action := permission[:separator], permission[separator+1:]
		addGrant(resource, action)
	}
	bundle := identitysdk.AccessBundle{
		ContractVersion: identitysdk.CurrentPolicyBundleVersion, AuthorizationRevision: "upload-test-authorization",
		ExpiresAt: time.Now().Add(time.Hour), Subject: identitysdk.Subject{WorkspaceID: "workspace-a", SubjectID: "user"},
		FunctionGrants: grants, DataPolicies: dataPolicies,
		FieldPolicies: []identitysdk.FieldPolicy{
			{Resource: "asset", Field: "file_url", Read: grantKeys["asset\x00read"], Write: grantKeys["asset\x00update"] || grantKeys["asset\x00create"]},
			{Resource: "document", Field: "file_url", Read: grantKeys["document\x00read"], Write: grantKeys["document\x00update"] || grantKeys["document\x00create"]},
			{Resource: "document", Field: "sensitive", Read: grantKeys["document\x00read"], Write: grantKeys["document\x00update"] || grantKeys["document\x00create"]},
		},
	}
	return principalmodel.Principal{Principal: identitysdk.Principal{
		Known: true, UserID: "user", WorkspaceID: "workspace-a", RoleKey: "upload-test",
		AuthorizationRevision: string(bundle.AuthorizationRevision), AccessBundle: &bundle,
	}}
}

func assertUploadAccessError(t *testing.T, err error, kind apperror.ErrorKind, code string) {
	t.Helper()
	if apperror.KindOf(err) != kind || apperror.CodeOf(err) != code {
		t.Fatalf("error kind=%s code=%q err=%v", apperror.KindOf(err), apperror.CodeOf(err), err)
	}
}

func TestUploadAccessAuthorizeUpload(t *testing.T) {
	audit := &uploadAccessAuditStub{}
	service := NewUploadAccessApplicationService(uploadAccessCatalog(), audit, nil)
	assertUploadAccessError(t, service.AuthorizeUpload(t.Context(), "asset", "file_url", principalmodel.Principal{}), apperror.KindForbidden, "backend.role.unknown")

	assertUploadAccessError(t, service.AuthorizeUpload(t.Context(), "missing", "file_url", uploadAccessPrincipal("asset.update")), apperror.KindNotFound, "backend.object.not_found")
	assertUploadAccessError(t, service.AuthorizeUpload(t.Context(), "asset", "missing", uploadAccessPrincipal("asset.update")), apperror.KindBadRequest, "backend.upload.field_not_defined")
	assertUploadAccessError(t, service.AuthorizeUpload(t.Context(), "asset", "file_url", uploadAccessPrincipal("asset.read")), apperror.KindForbidden, "backend.upload.permission_denied")
	if audit.reason != "permission" {
		t.Fatalf("audit reason=%q", audit.reason)
	}
	for _, permissions := range [][]string{{"asset.update"}, {"asset.create"}} {
		if err := service.AuthorizeUpload(t.Context(), " asset ", " file_url ", uploadAccessPrincipal(permissions...)); err != nil {
			t.Fatalf("permissions=%v err=%v", permissions, err)
		}
	}
	service.RecordUploaded(t.Context(), "asset", "file_url", "file.txt", "text/plain", 4, uploadAccessPrincipal("asset.update"))
	if audit.events[len(audit.events)-1] != "file_uploaded" {
		t.Fatalf("events=%v", audit.events)
	}
}

func TestUploadApplicationAuthorizesWorkspaceBeforePortAccess(t *testing.T) {
	catalog := &workspaceGuardUploadCatalog{}
	audit := &uploadAccessAuditStub{}
	records := &uploadAccessRecordStub{}
	service := NewUploadAccessApplicationService(catalog, audit, records)
	missing := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "user"}}
	for _, err := range []error{
		service.AuthorizeUpload(t.Context(), "asset", "file_url", missing),
		service.AuthorizeDownload(t.Context(), "asset", "file_url", "record", "file", missing),
	} {
		assertUploadAccessError(t, err, apperror.KindForbidden, "backend.workspace_scope_required")
	}
	service.RecordUploaded(t.Context(), "asset", "file_url", "file", "text/plain", 1, missing)
	if catalog.calls != 0 || len(audit.events) != 0 {
		t.Fatalf("ports called before workspace authorization: catalog=%d audit=%v", catalog.calls, audit.events)
	}
}

func TestUploadAccessAuthorizeDownloadWithoutRecord(t *testing.T) {
	audit := &uploadAccessAuditStub{}
	service := NewUploadAccessApplicationService(uploadAccessCatalog(), audit, nil)

	for _, test := range []struct {
		object, field, record, filename string
		principal                       principalmodel.Principal
		kind                            apperror.ErrorKind
		code, reason                    string
	}{
		{"", "file_url", "", "file.txt", uploadAccessPrincipal("asset.read"), apperror.KindForbidden, "backend.upload.permission_denied", "missing_context"},
		{"asset", "", "", "file.txt", uploadAccessPrincipal("asset.read"), apperror.KindForbidden, "backend.upload.permission_denied", "missing_context"},
		{"missing", "file_url", "", "file.txt", uploadAccessPrincipal("asset.read"), apperror.KindNotFound, "backend.object.not_found", "object"},
		{"asset", "missing", "", "file.txt", uploadAccessPrincipal("asset.read"), apperror.KindForbidden, "backend.upload.permission_denied", "permission"},
		{"asset", "file_url", "", "file.txt", uploadAccessPrincipal(), apperror.KindForbidden, "backend.upload.permission_denied", "permission"},
		{"document", "file_url", "", "file.txt", uploadAccessPrincipal("document.read"), apperror.KindForbidden, "backend.upload.permission_denied", "record_context"},
		{"asset", "file_url", "record", "file.txt", uploadAccessPrincipal("asset.read"), apperror.KindForbidden, "backend.upload.permission_denied", "record_service"},
	} {
		err := service.AuthorizeDownload(t.Context(), test.object, test.field, test.record, test.filename, test.principal)
		assertUploadAccessError(t, err, test.kind, test.code)
		if audit.reason != test.reason {
			t.Fatalf("case=%+v reason=%q", test, audit.reason)
		}
	}
	if err := service.AuthorizeDownload(t.Context(), "asset", "file_url", "", "file.txt", uploadAccessPrincipal("asset.read")); err != nil {
		t.Fatal(err)
	}
}

func TestUploadAccessAuthorizeDownloadAgainstRecord(t *testing.T) {
	audit := &uploadAccessAuditStub{}
	records := &uploadAccessRecordStub{record: recordmodel.Record{Data: map[string]any{"file_url": "/uploads/file.txt"}}}
	service := NewUploadAccessApplicationService(uploadAccessCatalog(), audit, records)
	principal := uploadAccessPrincipal("asset.read", "document.read")

	if err := service.AuthorizeDownload(t.Context(), "asset", "file_url", "record", "file.txt", principal); err != nil {
		t.Fatal(err)
	}
	if audit.events[len(audit.events)-1] != "file_downloaded" {
		t.Fatalf("events=%v", audit.events)
	}
	records.record.Data["file_url"] = "/uploads/other.txt"
	assertUploadAccessError(t, service.AuthorizeDownload(t.Context(), "asset", "file_url", "record", "file.txt", principal), apperror.KindForbidden, "backend.upload.permission_denied")
	records.err = errors.New("database unavailable")
	assertUploadAccessError(t, service.AuthorizeDownload(t.Context(), "asset", "file_url", "record", "file.txt", principal), apperror.KindForbidden, "backend.upload.permission_denied")

	records.err = nil
	for _, sensitive := range []any{true, " TRUE "} {
		records.record.Data = map[string]any{"file_url": "file:///tmp/document.pdf?download=1", "sensitive": sensitive}
		assertUploadAccessError(t, service.AuthorizeDownload(t.Context(), "document", "file_url", "record", "document.pdf", principal), apperror.KindForbidden, "backend.upload.permission_denied")
	}
	workspaceAdmin := uploadAccessPrincipal("document.read", "runtime.appschema.validate_application_definition")
	assertUploadAccessError(t, service.AuthorizeDownload(t.Context(), "document", "file_url", "record", "document.pdf", workspaceAdmin), apperror.KindForbidden, "backend.upload.permission_denied")
	privileged := uploadAccessPrincipal("document.read", "document.sensitive.read")
	if err := service.AuthorizeDownload(t.Context(), "document", "file_url", "record", "document.pdf", privileged); err != nil {
		t.Fatalf("exact sensitive permission rejected: %v", err)
	}
	records.record.Data["sensitive"] = false
	if err := service.AuthorizeDownload(t.Context(), "document", "file_url", "record", "document.pdf", principal); err != nil {
		t.Fatalf("non-sensitive document: %v", err)
	}
}

func TestUploadAccessPolicyHelpers(t *testing.T) {
	for _, test := range []struct {
		value any
		want  bool
	}{{true, true}, {false, false}, {"true", true}, {"false", false}, {1, false}} {
		if got := uploadRecordBoolean(test.value); got != test.want {
			t.Fatalf("recordBoolean(%v)=%v", test.value, got)
		}
	}
	for _, test := range []struct {
		value any
		want  bool
	}{{"", false}, {"/uploads/a.pdf", true}, {"file:///tmp/a.pdf?x=1", true}, {"/uploads/b.pdf", false}} {
		if got := uploadFileMatchesRecord("a.pdf", test.value); got != test.want {
			t.Fatalf("fileMatchesRecord(%v)=%v", test.value, got)
		}
	}
}
