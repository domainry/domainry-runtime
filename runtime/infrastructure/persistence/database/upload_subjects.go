package database

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	foundationartifact "github.com/domainry/domainry-foundation/artifact"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	"github.com/domainry/domainry-orm/query"
	uploadapplication "github.com/domainry/domainry-runtime/runtime/application/upload"
)

const uploadSubjectResourceType = "identity_user"

type uploadSubjectBindingMetadata struct {
	Filename  string `json:"filename"`
	ObjectKey string `json:"object_key"`
	SHA256    string `json:"sha256"`
}

func (s *RuntimeStore) InsertUploadSubject(ctx context.Context, value uploadapplication.UploadSubjectBinding) error {
	for _, field := range []string{value.WorkspaceID, value.FileID, value.Filename, value.ObjectKey, value.FieldKey, value.UserID, value.SHA256} {
		if strings.TrimSpace(field) == "" {
			return fmt.Errorf("upload subject binding is incomplete")
		}
	}
	metadata, err := json.Marshal(uploadSubjectBindingMetadata{Filename: value.Filename, ObjectKey: value.ObjectKey, SHA256: value.SHA256})
	if err != nil {
		return err
	}
	columns := []string{"id", "artifact_id", "owner", "kind", "resource_type", "resource_id", "field_key", "metadata_json", "created_at"}
	values := []any{uploadSubjectBindingID(value.WorkspaceID, value.Filename), value.FileID, foundationartifact.OwnerUploads, foundationartifact.BindingSubject, uploadSubjectResourceType, value.UserID, value.FieldKey, string(metadata), time.Now().UTC().Format(time.RFC3339Nano)}
	builder, err := s.SubjectEvidenceInsertBuilder(value.WorkspaceID, "_artifact_bindings", columns, values, s.SubjectActorWriteAllowed(value.WorkspaceID, value.UserID))
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
	statement, args, err := query.NewWorkspaceSelectBuilder(s.SQLRenderer, "_artifact_bindings", workspace).
		Columns("workspace_id", "artifact_id", "resource_id", "field_key", "metadata_json").
		Where(query.And(
			query.Equal("owner", foundationartifact.OwnerUploads),
			query.Equal("kind", foundationartifact.BindingSubject),
			query.Equal("resource_type", uploadSubjectResourceType),
			query.Or(query.Equal("artifact_id", identifier), query.Equal("id", uploadSubjectBindingID(workspace, identifier))),
		)).Limit(1).Build()
	if err != nil {
		return value, err
	}
	var metadata string
	if err = s.db.QueryRowContext(ctx, statement, args...).Scan(&value.WorkspaceID, &value.FileID, &value.UserID, &value.FieldKey, &metadata); err != nil {
		return value, err
	}
	var decoded uploadSubjectBindingMetadata
	if err = json.Unmarshal([]byte(metadata), &decoded); err != nil {
		return value, fmt.Errorf("decode upload subject binding: %w", err)
	}
	value.Filename, value.ObjectKey, value.SHA256 = decoded.Filename, decoded.ObjectKey, decoded.SHA256
	return value, nil
}

// SubjectUploadReferences includes abandoned, replaced and current uploads.
// Exceeding the bounded inventory fails rather than silently omitting files.
func (s *RuntimeStore) SubjectUploadReferences(ctx context.Context, workspace, subject string) ([]lifecyclecontract.SubjectFileReference, error) {
	if workspace == "" || subject == "" {
		return nil, fmt.Errorf("upload subject scope is required")
	}
	statement, args, err := query.NewWorkspaceSelectBuilder(s.SQLRenderer, "_artifact_bindings", workspace).
		Columns("id", "metadata_json").Where(query.And(
		query.Equal("owner", foundationartifact.OwnerUploads),
		query.Equal("kind", foundationartifact.BindingSubject),
		query.Equal("resource_type", uploadSubjectResourceType),
		query.Equal("resource_id", subject),
	)).OrderBy(query.Ascending("id")).Limit(10001).Build()
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
		var id, metadata string
		if err = rows.Scan(&id, &metadata); err != nil {
			return nil, err
		}
		var decoded uploadSubjectBindingMetadata
		if err = json.Unmarshal([]byte(metadata), &decoded); err != nil {
			return nil, fmt.Errorf("decode upload subject binding: %w", err)
		}
		if strings.TrimSpace(decoded.Filename) == "" {
			return nil, fmt.Errorf("decode upload subject binding: filename is required")
		}
		refs = append(refs, lifecyclecontract.SubjectFileReference{WorkspaceID: workspace, ObjectKey: "_artifact_bindings", RecordID: id, FieldKey: "metadata_json", Reference: "/uploads/" + decoded.Filename})
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(refs) > 10000 {
		return nil, fmt.Errorf("upload subject inventory limit exceeded")
	}
	return refs, nil
}

func uploadSubjectBindingID(workspace, filename string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(workspace) + "\x00" + strings.TrimSpace(filename)))
	return "upload-subject-" + hex.EncodeToString(digest[:])
}
