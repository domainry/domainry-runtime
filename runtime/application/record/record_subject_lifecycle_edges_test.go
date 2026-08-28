package record

import (
	"context"
	"encoding/json"
	"errors"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	"reflect"
	"strings"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	lifecyclecontract "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/contract"
	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
)

type subjectLifecycleRepository struct {
	recordrepository.RecordRepository
	records   []recordmodel.Record
	listErr   error
	updateErr error
	updated   []recordmodel.Record
	pages     int
	hasNext   bool
	queries   []recordmodel.RecordListQuery
}

func TestAppendUniqueRecordSubjectFieldSkipsBlankCandidate(t *testing.T) {
	fields := []string{"user_id"}
	got := appendUniqueRecordSubjectField(fields, " ")
	if len(got) != 1 || got[0] != "user_id" {
		t.Fatalf("fields=%v", got)
	}
}

func (r *subjectLifecycleRepository) ListRecords(_ context.Context, _ string, _ definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	if r.listErr != nil {
		return recordmodel.RecordPageResult{}, r.listErr
	}
	r.pages++
	r.queries = append(r.queries, query)
	if query.Page > 1 || query.AfterID != "" {
		return recordmodel.RecordPageResult{}, nil
	}
	return recordmodel.RecordPageResult{Items: append([]recordmodel.Record(nil), r.records...), HasNext: r.hasNext}, nil
}

func (r *subjectLifecycleRepository) UpdateRecord(_ context.Context, _ string, _ definitionmodel.ObjectSchema, record recordmodel.Record) error {
	r.updated = append(r.updated, record)
	return r.updateErr
}

type subjectLifecycleFileStore struct {
	exportErr error
	deleteErr error
	exported  []lifecyclecontract.SubjectFileReference
	deleted   []lifecyclecontract.SubjectFileReference
}

func (s *subjectLifecycleFileStore) ExportSubjectFile(_ context.Context, reference lifecyclecontract.SubjectFileReference) (lifecyclecontract.SubjectFileEvidence, error) {
	s.exported = append(s.exported, reference)
	return lifecyclecontract.SubjectFileEvidence{Reference: reference.Reference, Content: []byte("evidence")}, s.exportErr
}

func (s *subjectLifecycleFileStore) DeleteSubjectFile(_ context.Context, reference lifecyclecontract.SubjectFileReference) (lifecyclecontract.SubjectFileEvidence, error) {
	s.deleted = append(s.deleted, reference)
	return lifecyclecontract.SubjectFileEvidence{Reference: reference.Reference}, s.deleteErr
}

func subjectLifecycleObject() definitionmodel.ObjectSchema {
	return definitionmodel.ObjectSchema{Key: "employee_document", Fields: []definitionmodel.FieldSchema{
		{Key: "employee", Type: "user"},
		{Key: "owner", Type: "user"},
		{Key: "email", Type: "email", Config: map[string]any{"lifecycle_erase": "anonymize"}},
		{Key: "attachments", Type: "text", Config: map[string]any{"lifecycle_subject_file": true, "lifecycle_erase": "delete"}},
		{Key: "notes", Type: "text", Config: map[string]any{"lifecycle_erase": "retain"}},
	}}
}

func TestRecordSubjectOwnerAndPreviewCountsDeduplicatedRecordsAndFiles(t *testing.T) {
	object := subjectLifecycleObject()
	repository := &subjectLifecycleRepository{records: []recordmodel.Record{{ID: "document-1", Data: map[string]any{
		"employee": "user-1", "owner": "user-1", "attachments": []any{" file-a ", []string{"file-b", " "}},
	}}}}
	service := NewRecordSubjectLifecycleApplicationService(repository, []definitionmodel.ObjectSchema{object}, nil)
	if owner := service.Owner(t.Context()); owner != "record" {
		t.Fatalf("owner=%q", owner)
	}
	raw, err := service.PreviewSubject(t.Context(), "workspace-a", "user-1")
	if err != nil {
		t.Fatal(err)
	}
	var preview struct {
		Objects map[string]int `json:"objects"`
		Files   int            `json:"files"`
	}
	if err := json.Unmarshal(raw, &preview); err != nil {
		t.Fatal(err)
	}
	if preview.Objects[object.Key] != 1 || preview.Files != 2 || repository.pages != 2 {
		t.Fatalf("preview=%#v pages=%d", preview, repository.pages)
	}
}

func TestRecordSubjectLifecycleDiscoversProfileExtensionRelation(t *testing.T) {
	profile := definitionmodel.ObjectSchema{Key: "member_profile", Fields: []definitionmodel.FieldSchema{
		{Key: "identity_user", Type: "relation"},
		{Key: "email", Type: "email", Config: map[string]any{"lifecycle_erase": "anonymize"}},
	}}
	repository := &subjectLifecycleRepository{records: []recordmodel.Record{{
		ID: "member-1", Data: map[string]any{"identity_user": "user-1", "email": "member@example.com"},
	}}}
	service := NewRecordSubjectLifecycleApplicationService(repository, []definitionmodel.ObjectSchema{profile}, nil, []profilebindingmodel.Binding{
		{ObjectKey: "member_profile", IdentityRelationField: "identity_user"},
		{ObjectKey: "member_profile", IdentityRelationField: " identity_user "},
		{ObjectKey: "member_profile", IdentityRelationField: " "},
		{},
	})

	exported, err := service.ExportSubject(t.Context(), "workspace-a", "user-1")
	if err != nil || !strings.Contains(string(exported), `"object_key":"member_profile"`) {
		t.Fatalf("profile export=%s err=%v", exported, err)
	}
	if len(repository.queries) != 1 || repository.queries[0].Filters["identity_user"] != "user-1" {
		t.Fatalf("profile relation queries=%#v", repository.queries)
	}
	if _, err := service.EraseSubject(t.Context(), "workspace-a", "user-1", nil); err != nil {
		t.Fatal(err)
	}
	if len(repository.updated) != 1 || repository.updated[0].Data["email"] == "member@example.com" {
		t.Fatalf("profile was not anonymized: %#v", repository.updated)
	}
}

func TestRecordSubjectRepositoryAndFileExportFailures(t *testing.T) {
	object := subjectLifecycleObject()
	failure := errors.New("subject dependency failed")
	if _, err := (*RecordSubjectLifecycleApplicationService)(nil).PreviewSubject(t.Context(), "workspace-a", "user-1"); err == nil || !strings.Contains(err.Error(), "repository unavailable") {
		t.Fatalf("nil repository err=%v", err)
	}
	if _, err := (&RecordSubjectLifecycleApplicationService{}).PreviewSubject(t.Context(), "workspace-a", "user-1"); err == nil || !strings.Contains(err.Error(), "repository unavailable") {
		t.Fatalf("missing repository err=%v", err)
	}
	service := NewRecordSubjectLifecycleApplicationService(&subjectLifecycleRepository{listErr: failure}, []definitionmodel.ObjectSchema{object}, nil)
	if _, err := service.ExportSubject(t.Context(), "workspace-a", "user-1"); !errors.Is(err, failure) {
		t.Fatalf("list err=%v", err)
	}

	repository := &subjectLifecycleRepository{records: []recordmodel.Record{{ID: "document-1", Data: map[string]any{"employee": "user-1", "attachments": "file-a"}}}}
	service = NewRecordSubjectLifecycleApplicationService(repository, []definitionmodel.ObjectSchema{object}, nil)
	if _, err := service.ExportSubject(t.Context(), "workspace-a", "user-1"); err == nil || !strings.Contains(err.Error(), "file store unavailable") {
		t.Fatalf("missing file store err=%v", err)
	}
	files := &subjectLifecycleFileStore{exportErr: failure}
	service = NewRecordSubjectLifecycleApplicationService(repository, []definitionmodel.ObjectSchema{object}, files)
	if _, err := service.ExportSubject(t.Context(), "workspace-a", "user-1"); !errors.Is(err, failure) || len(files.exported) != 1 {
		t.Fatalf("export err=%v exported=%#v", err, files.exported)
	}
}

func TestRecordSubjectEmptyObjectsPaginationAndEraseValueEdges(t *testing.T) {
	noIdentity := definitionmodel.ObjectSchema{Key: "settings", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	service := NewRecordSubjectLifecycleApplicationService(&subjectLifecycleRepository{}, []definitionmodel.ObjectSchema{noIdentity}, nil)
	preview, err := service.PreviewSubject(t.Context(), "workspace-a", "user-1")
	if err != nil || !strings.Contains(string(preview), `"objects":{}`) {
		t.Fatalf("empty preview=%s err=%v", preview, err)
	}
	exported, err := service.ExportSubject(t.Context(), "workspace-a", "user-1")
	if err != nil || !strings.Contains(string(exported), `"objects":[]`) {
		t.Fatalf("empty export=%s err=%v", exported, err)
	}

	object := definitionmodel.ObjectSchema{Key: "profile", Fields: []definitionmodel.FieldSchema{
		{Key: "employee", Type: "user"},
		{Key: "missing", Type: "text", Config: map[string]any{"lifecycle_erase": "anonymize"}},
		{Key: "nil_value", Type: "text", Config: map[string]any{"lifecycle_erase": "anonymize"}},
		{Key: "empty_value", Type: "text", Config: map[string]any{"lifecycle_erase": "anonymize"}},
		{Key: "file_token", Type: "text", Config: map[string]any{"lifecycle_subject_file": true, "lifecycle_erase": "anonymize"}},
		{Key: "delete_value", Type: "text", Config: map[string]any{"lifecycle_erase": "delete"}},
	}}
	repository := &subjectLifecycleRepository{records: []recordmodel.Record{{ID: "profile-1", Data: map[string]any{
		"employee": "user-1", "nil_value": nil, "empty_value": " ", "file_token": "file-a", "delete_value": "secret",
	}}}, hasNext: true}
	service = NewRecordSubjectLifecycleApplicationService(repository, []definitionmodel.ObjectSchema{object}, nil)
	if _, err := service.PreviewSubject(t.Context(), "workspace-a", "user-1"); err != nil || repository.pages != 2 {
		t.Fatalf("paginated preview pages=%d err=%v", repository.pages, err)
	}
	if _, err := service.EraseSubject(t.Context(), "workspace-a", "user-1", nil); err != nil || len(repository.updated) != 1 {
		t.Fatalf("erase updated=%#v err=%v", repository.updated, err)
	}
	if got := repository.updated[0].Data["file_token"]; !strings.HasPrefix(got.(string), "erased-") {
		t.Fatalf("anonymized file token=%#v", got)
	}
	if repository.updated[0].Data["delete_value"] != nil {
		t.Fatalf("deleted scalar=%#v", repository.updated[0].Data["delete_value"])
	}

	unchanged := definitionmodel.ObjectSchema{Key: "identity", Fields: []definitionmodel.FieldSchema{{Key: "employee", Type: "user"}}}
	repository = &subjectLifecycleRepository{records: []recordmodel.Record{{ID: "identity-1", Data: map[string]any{"employee": "user-1"}}}}
	service = NewRecordSubjectLifecycleApplicationService(repository, []definitionmodel.ObjectSchema{unchanged}, nil)
	if _, err := service.EraseSubject(t.Context(), "workspace-a", "user-1", nil); err != nil || len(repository.updated) != 0 {
		t.Fatalf("unchanged updated=%#v err=%v", repository.updated, err)
	}

	failure := errors.New("list failed")
	service = NewRecordSubjectLifecycleApplicationService(&subjectLifecycleRepository{listErr: failure}, []definitionmodel.ObjectSchema{object}, nil)
	if _, err := service.EraseSubject(t.Context(), "workspace-a", "user-1", nil); !errors.Is(err, failure) {
		t.Fatalf("erase list err=%v", err)
	}
}

func TestRecordSubjectEraseLegalHoldUpdateAndFileFailures(t *testing.T) {
	object := subjectLifecycleObject()
	newRecord := func() recordmodel.Record {
		return recordmodel.Record{ID: "document-1", Data: map[string]any{
			"employee": "user-1", "email": "person@example.com", "attachments": "file-a", "notes": "retain",
		}}
	}
	service := NewRecordSubjectLifecycleApplicationService(&subjectLifecycleRepository{records: []recordmodel.Record{newRecord()}}, []definitionmodel.ObjectSchema{object}, nil)
	if _, err := service.EraseSubject(t.Context(), "workspace-a", "user-1", []lifecyclemodel.LegalHold{{}}); err == nil || !strings.Contains(err.Error(), "legal hold") {
		t.Fatalf("legal hold err=%v", err)
	}

	failure := errors.New("subject mutation failed")
	repository := &subjectLifecycleRepository{records: []recordmodel.Record{newRecord()}, updateErr: failure}
	service = NewRecordSubjectLifecycleApplicationService(repository, []definitionmodel.ObjectSchema{object}, nil)
	if _, err := service.EraseSubject(t.Context(), "workspace-a", "user-1", nil); !errors.Is(err, failure) {
		t.Fatalf("update err=%v", err)
	}

	repository = &subjectLifecycleRepository{records: []recordmodel.Record{newRecord()}}
	service = NewRecordSubjectLifecycleApplicationService(repository, []definitionmodel.ObjectSchema{object}, nil)
	if _, err := service.EraseSubject(t.Context(), "workspace-a", "user-1", nil); err == nil || !strings.Contains(err.Error(), "file store unavailable") || len(repository.updated) != 1 {
		t.Fatalf("missing file store err=%v updated=%d", err, len(repository.updated))
	}

	files := &subjectLifecycleFileStore{deleteErr: failure}
	repository = &subjectLifecycleRepository{records: []recordmodel.Record{newRecord()}}
	service = NewRecordSubjectLifecycleApplicationService(repository, []definitionmodel.ObjectSchema{object}, files)
	if _, err := service.EraseSubject(t.Context(), "workspace-a", "user-1", nil); !errors.Is(err, failure) || len(files.deleted) != 1 {
		t.Fatalf("delete err=%v deleted=%#v", err, files.deleted)
	}
}

func TestRecordSubjectFileValuesAndAnonymousValues(t *testing.T) {
	values := recordSubjectFileValues([]any{" a ", []string{"b", " "}, 3, nil})
	if !reflect.DeepEqual(values, []string{"a", "b"}) {
		t.Fatalf("values=%#v", values)
	}
	if values := recordSubjectFileValues(map[string]any{"path": "ignored"}); len(values) != 0 {
		t.Fatalf("unsupported values=%#v", values)
	}
	emailField := definitionmodel.FieldSchema{Key: "email", Type: " EMAIL "}
	first := recordSubjectAnonymousValue("workspace-a", "user-1", "customer", "customer-1", emailField)
	second := recordSubjectAnonymousValue("workspace-a", "user-1", "customer", "customer-1", emailField)
	text := recordSubjectAnonymousValue("workspace-a", "user-1", "customer", "customer-1", definitionmodel.FieldSchema{Key: "name", Type: "text"})
	if first != second || !strings.HasPrefix(first, "erased+") || !strings.HasSuffix(first, "@invalid.local") || !strings.HasPrefix(text, "erased-") {
		t.Fatalf("email=%q second=%q text=%q", first, second, text)
	}
}
