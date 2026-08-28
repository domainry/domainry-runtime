package record

import (
	identitysdk "github.com/domainry/domainry-identity-sdk"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"context"
	"errors"
	"reflect"
	"strconv"
	"testing"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type ownerDepartmentRepositoryProbe struct {
	recordrepository.RecordRepository
	pages   map[string][]recordmodel.RecordPageResult
	err     error
	queries []string
}

func (r *ownerDepartmentRepositoryProbe) ListRecords(_ context.Context, _ string, object definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	r.queries = append(r.queries, object.Key+":"+strconv.Itoa(query.Page)+":"+query.AfterID)
	if r.err != nil {
		return recordmodel.RecordPageResult{}, r.err
	}
	pages := r.pages[object.Key]
	pageIndex := query.Page - 1
	if query.AfterID != "" {
		pageIndex++
	}
	if pageIndex < 0 || pageIndex >= len(pages) {
		return recordmodel.RecordPageResult{}, nil
	}
	return pages[pageIndex], nil
}

func TestOwnerDepartmentPathRebuilderUpdatesEligibleRecords(t *testing.T) {
	objects := map[string]definitionmodel.ObjectSchema{
		"task": ownerDepartmentObject("task"),
		"note": {Key: "note", Fields: []definitionmodel.FieldSchema{{Key: "owner", Type: "user"}}},
	}
	repository := &ownerDepartmentRepositoryProbe{pages: map[string][]recordmodel.RecordPageResult{
		"task": {
			{Items: []recordmodel.Record{
				{ID: "task-1", Data: map[string]any{"owner": "u1"}},
				{ID: "task-2", Data: map[string]any{"owner": "u2"}},
			}, HasNext: true},
			{Items: []recordmodel.Record{{ID: "task-3", Data: map[string]any{"owner": "u1", "owner_department_id": "sales", "owner_department_path": "/company/sales"}}}},
		},
	}}
	updatedRecords := []recordmodel.Record{}
	rebuilder := NewRecordOwnerDepartmentPathApplicationService(repository, func() map[string]definitionmodel.ObjectSchema { return objects }, func(_ context.Context, _ string, _ definitionmodel.ObjectSchema, record recordmodel.Record, reason string) error {
		if reason != "rebuild owner department path" {
			t.Fatalf("reason = %q", reason)
		}
		updatedRecords = append(updatedRecords, record)
		return nil
	})

	updated, err := rebuilder.Rebuild(t.Context(), "default", []identitysdk.WorkforceEntry{
		{IdentityUserID: "u1", OrganizationUnitID: "sales", OrganizationPath: "/company/sales"},
		{IdentityUserID: "u2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated != 1 || len(updatedRecords) != 1 || updatedRecords[0].ID != "task-1" {
		t.Fatalf("updated=%d records=%#v", updated, updatedRecords)
	}
	if updatedRecords[0].Data["owner_department_id"] != "sales" || updatedRecords[0].Data["owner_department_path"] != "/company/sales" || updatedRecords[0].UpdatedAt == "" {
		t.Fatalf("updated record = %#v", updatedRecords[0])
	}
	if !reflect.DeepEqual(repository.queries, []string{"task:1:", "task:1:task-2"}) {
		t.Fatalf("queries = %#v", repository.queries)
	}
}

func TestOwnerDepartmentPathRebuilderPreservesPartialCountAndErrors(t *testing.T) {
	object := ownerDepartmentObject("task")
	repository := &ownerDepartmentRepositoryProbe{pages: map[string][]recordmodel.RecordPageResult{"task": {{Items: []recordmodel.Record{
		{ID: "task-1", Data: map[string]any{"owner": "u1"}},
		{ID: "task-2", Data: map[string]any{"owner": "u1"}},
	}}}}}
	updateErr := errors.New("update failed")
	calls := 0
	rebuilder := NewRecordOwnerDepartmentPathApplicationService(repository, func() map[string]definitionmodel.ObjectSchema {
		return map[string]definitionmodel.ObjectSchema{"task": object}
	}, func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, string) error {
		calls++
		if calls == 2 {
			return updateErr
		}
		return nil
	})
	updated, err := rebuilder.Rebuild(t.Context(), "default", []identitysdk.WorkforceEntry{{IdentityUserID: "u1", OrganizationUnitID: "sales", OrganizationPath: "/company/sales"}})
	if !errors.Is(err, updateErr) || updated != 1 {
		t.Fatalf("updated=%d err=%v", updated, err)
	}

	repository = &ownerDepartmentRepositoryProbe{err: errors.New("store unavailable")}
	rebuilder = NewRecordOwnerDepartmentPathApplicationService(repository, func() map[string]definitionmodel.ObjectSchema {
		return map[string]definitionmodel.ObjectSchema{"task": object}
	}, nil)
	updated, err = rebuilder.Rebuild(t.Context(), "default", []identitysdk.WorkforceEntry{{IdentityUserID: "u1", OrganizationUnitID: "sales", OrganizationPath: "/company/sales"}})
	if updated != 0 {
		t.Fatalf("updated before repository error = %d", updated)
	}
	assertRecordApplicationError(t, err, apperror.KindInternal, "backend.internal", map[string]string{"operation": "list records for owner department rebuild"})
}

func TestOwnerDepartmentPathRebuilderSkipsEmptyUsers(t *testing.T) {
	repository := &ownerDepartmentRepositoryProbe{}
	rebuilder := NewRecordOwnerDepartmentPathApplicationService(repository, func() map[string]definitionmodel.ObjectSchema {
		return map[string]definitionmodel.ObjectSchema{"task": ownerDepartmentObject("task")}
	}, nil)
	updated, err := rebuilder.Rebuild(t.Context(), "default", []identitysdk.WorkforceEntry{{IdentityUserID: ""}})
	if err != nil || updated != 0 || len(repository.queries) != 0 {
		t.Fatalf("updated=%d queries=%#v err=%v", updated, repository.queries, err)
	}
}

func TestOwnerDepartmentPathRebuilderConditionEdges(t *testing.T) {
	objects := map[string]definitionmodel.ObjectSchema{
		"missing_owner":           {Key: "missing_owner", Fields: []definitionmodel.FieldSchema{{Key: "owner_department_id"}, {Key: "owner_department_path"}}},
		"missing_department_id":   {Key: "missing_department_id", Fields: []definitionmodel.FieldSchema{{Key: "owner", Type: "user"}, {Key: "owner_department_path"}}},
		"missing_department_path": {Key: "missing_department_path", Fields: []definitionmodel.FieldSchema{{Key: "owner", Type: "user"}, {Key: "owner_department_id"}}},
		"task":                    ownerDepartmentObject("task"),
	}
	repository := &ownerDepartmentRepositoryProbe{pages: map[string][]recordmodel.RecordPageResult{"task": {{Items: []recordmodel.Record{
		{ID: "unknown-owner", Data: map[string]any{"owner": "missing"}},
		{ID: "blank-path", Data: map[string]any{"owner": "u2"}},
		{ID: "stale-path", Data: map[string]any{"owner": "u1", "owner_department_id": "sales", "owner_department_path": "/old"}},
	}}}}}
	updated := 0
	rebuilder := NewRecordOwnerDepartmentPathApplicationService(repository, func() map[string]definitionmodel.ObjectSchema { return objects }, func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, string) error {
		updated++
		return nil
	})
	count, err := rebuilder.Rebuild(t.Context(), "default", []identitysdk.WorkforceEntry{
		{IdentityUserID: "u1", OrganizationUnitID: "sales", OrganizationPath: "/company/sales"},
		{IdentityUserID: "u2", OrganizationUnitID: "sales", OrganizationPath: " "},
	})
	if err != nil || count != 1 || updated != 1 {
		t.Fatalf("count=%d updated=%d err=%v", count, updated, err)
	}
	if got := recordStringValue(recordmodel.Record{Data: map[string]any{"field": nil}}, "field"); got != "" {
		t.Fatalf("nil string value=%q", got)
	}
}

func TestOwnerDepartmentPathRebuilderRejectsMissingWorkspaceBeforeRepository(t *testing.T) {
	repository := &ownerDepartmentRepositoryProbe{}
	rebuilder := NewRecordOwnerDepartmentPathApplicationService(repository, func() map[string]definitionmodel.ObjectSchema {
		return map[string]definitionmodel.ObjectSchema{"task": ownerDepartmentObject("task")}
	}, nil)
	updated, err := rebuilder.Rebuild(t.Context(), "", []identitysdk.WorkforceEntry{{IdentityUserID: "u1"}})
	if !errors.Is(err, principalmodel.ErrWorkspaceIDRequired) || updated != 0 || len(repository.queries) != 0 {
		t.Fatalf("updated=%d queries=%#v err=%v", updated, repository.queries, err)
	}
}

func ownerDepartmentObject(key string) definitionmodel.ObjectSchema {
	return definitionmodel.ObjectSchema{Key: key, Fields: []definitionmodel.FieldSchema{
		{Key: "owner", Type: "user"},
		{Key: "owner_department_id", Type: "text"},
		{Key: "owner_department_path", Type: "text"},
	}}
}
