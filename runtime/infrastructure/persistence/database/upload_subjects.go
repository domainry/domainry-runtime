package database

import (
	"context"
	"fmt"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	"github.com/domainry/domainry-orm/query"
	uploadapplication "github.com/domainry/domainry-runtime/runtime/application/upload"
	"strings"
)

func (s *RuntimeStore) InsertUploadSubject(ctx context.Context, value uploadapplication.UploadSubjectBinding) error {
	for _, field := range []string{value.WorkspaceID, value.FileID, value.Filename, value.ObjectKey, value.FieldKey, value.UserID, value.SHA256} {
		if strings.TrimSpace(field) == "" {
			return fmt.Errorf("upload subject binding is incomplete")
		}
	}
	columns := []string{"id", "filename", "object_key", "field_key", "user_id", "sha256"}
	values := []any{value.FileID, value.Filename, value.ObjectKey, value.FieldKey, value.UserID, value.SHA256}
	builder, err := s.SubjectEvidenceInsertBuilder(value.WorkspaceID, "_upload_subject_bindings", columns, values)
	if err != nil {
		return err
	}
	statement, args, err := builder.Build()
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, statement, args...)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return fmt.Errorf("runtime.subject_erased")
	}
	return nil
}

func (s *RuntimeStore) FindUploadSubject(ctx context.Context, workspace, identifier string) (uploadapplication.UploadSubjectBinding, error) {
	var value uploadapplication.UploadSubjectBinding
	if strings.TrimSpace(workspace) == "" || strings.TrimSpace(identifier) == "" {
		return value, fmt.Errorf("upload subject scope is required")
	}
	statement, args, err := query.NewWorkspaceSelectBuilder(s.SQLRenderer, "_upload_subject_bindings", workspace).
		Columns("workspace_id", "id", "filename", "object_key", "field_key", "user_id", "sha256").
		Where(query.Or(query.Equal("id", identifier), query.Equal("filename", identifier))).Limit(1).Build()
	if err != nil {
		return value, err
	}
	err = s.db.QueryRowContext(ctx, statement, args...).Scan(&value.WorkspaceID, &value.FileID, &value.Filename, &value.ObjectKey, &value.FieldKey, &value.UserID, &value.SHA256)
	return value, err
}

// SubjectUploadReferences includes abandoned, replaced and current uploads.
// Exceeding the bounded inventory fails rather than silently omitting files.
func (s *RuntimeStore) SubjectUploadReferences(ctx context.Context, workspace, subject string) ([]lifecyclecontract.SubjectFileReference, error) {
	if workspace == "" || subject == "" {
		return nil, fmt.Errorf("upload subject scope is required")
	}
	statement, args, err := query.NewWorkspaceSelectBuilder(s.SQLRenderer, "_upload_subject_bindings", workspace).
		Columns("id", "filename").Where(query.Equal("user_id", subject)).OrderBy(query.Ascending("id")).Limit(10001).Build()
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	refs := []lifecyclecontract.SubjectFileReference{}
	for rows.Next() {
		var id, filename string
		if err = rows.Scan(&id, &filename); err != nil {
			return nil, err
		}
		refs = append(refs, lifecyclecontract.SubjectFileReference{WorkspaceID: workspace, ObjectKey: "_upload_subject_bindings", RecordID: id, FieldKey: "filename", Reference: "/uploads/" + filename})
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(refs) > 10000 {
		return nil, fmt.Errorf("upload subject inventory limit exceeded")
	}
	return refs, nil
}
