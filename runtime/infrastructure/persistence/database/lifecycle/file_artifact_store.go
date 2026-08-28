package lifecycle

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	lifecyclecontract "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/contract"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	requestcontext "github.com/domainry/domainry-runtime/runtime/platform/requestcontext"
)

const uploadArtifactGracePeriod = 24 * time.Hour

type FileArtifactStore struct {
	store      *database.RuntimeStore
	db         *sql.DB
	objects    map[string]definitionmodel.ObjectSchema
	uploadRoot string
	removeFile func(string) error
	absPath    func(string) (string, error)
	relPath    func(string, string) (string, error)
	contextErr func(context.Context) error
}

func NewFileArtifactStore(store *database.RuntimeStore, objects []definitionmodel.ObjectSchema, uploadRoot string) *FileArtifactStore {
	byKey := make(map[string]definitionmodel.ObjectSchema, len(objects))
	for _, object := range objects {
		byKey[strings.TrimSpace(object.Key)] = object
	}
	return &FileArtifactStore{store: store, db: store.DB(), objects: byKey, uploadRoot: uploadRoot, removeFile: os.Remove, absPath: filepath.Abs, relPath: filepath.Rel, contextErr: func(ctx context.Context) error { return ctx.Err() }}
}

func (s *FileArtifactStore) RegisterUpload(ctx context.Context, artifact lifecyclecontract.UploadArtifact) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("upload artifact store unavailable")
	}
	workspace, err := principalmodel.NewWorkspaceID(artifact.WorkspaceID)
	if err != nil {
		return err
	}
	artifact.WorkspaceID = workspace.String()
	artifact.ID, artifact.ObjectKey, artifact.FieldKey, artifact.Filename = strings.TrimSpace(artifact.ID), strings.TrimSpace(artifact.ObjectKey), strings.TrimSpace(artifact.FieldKey), strings.TrimSpace(artifact.Filename)
	if artifact.ID == "" {
		artifact.ID = requestcontext.NewRequestID()
	}
	object, ok := s.objects[artifact.ObjectKey]
	if !ok || !lifecycleObjectHasField(object, artifact.FieldKey) {
		return fmt.Errorf("upload artifact owner field is not declared")
	}
	if artifact.ID == "" || artifact.Filename == "" || filepath.Base(artifact.Filename) != artifact.Filename || artifact.SHA256 == "" || artifact.Size < 0 || artifact.CreatedAt.IsZero() {
		return fmt.Errorf("upload artifact evidence is incomplete")
	}
	createdAt := artifact.CreatedAt.UTC().Format(time.RFC3339Nano)
	var id string
	query := "SELECT " + s.store.Identifier("id") + " FROM " + s.store.TableIdentifier("lifecycle_file_artifacts") + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(1) + " AND " + s.store.Identifier("filename") + " = " + s.store.Placeholder(2)
	err = s.db.QueryRowContext(ctx, query, artifact.WorkspaceID, artifact.Filename).Scan(&id)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if err == nil {
		update := "UPDATE " + s.store.TableIdentifier("lifecycle_file_artifacts") + " SET " + s.store.Identifier("object_key") + " = " + s.store.Placeholder(1) + ", " + s.store.Identifier("field_key") + " = " + s.store.Placeholder(2) + ", " + s.store.Identifier("content_type") + " = " + s.store.Placeholder(3) + ", " + s.store.Identifier("sha256") + " = " + s.store.Placeholder(4) + ", " + s.store.Identifier("size_bytes") + " = " + s.store.Placeholder(5) + ", " + s.store.Identifier("status") + " = 'staged', " + s.store.Identifier("scan_status") + " = 'pending', " + s.store.Identifier("scan_provider") + " = '', " + s.store.Identifier("scan_evidence_ref") + " = '', " + s.store.Identifier("scanned_at") + " = '', " + s.store.Identifier("created_at") + " = " + s.store.Placeholder(6) + ", " + s.store.Identifier("last_referenced_at") + " = '', " + s.store.Identifier("delete_after") + " = " + s.store.Placeholder(7) + ", " + s.store.Identifier("deleted_at") + " = '' WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(8) + " AND " + s.store.Identifier("id") + " = " + s.store.Placeholder(9)
		_, err = s.db.ExecContext(ctx, update, artifact.ObjectKey, artifact.FieldKey, artifact.ContentType, artifact.SHA256, artifact.Size, createdAt, artifact.CreatedAt.UTC().Add(uploadArtifactGracePeriod).Format(time.RFC3339Nano), artifact.WorkspaceID, id)
		return err
	}
	columns := []string{"id", "workspace_id", "object_key", "field_key", "filename", "content_type", "sha256", "size_bytes", "status", "scan_status", "scan_provider", "scan_evidence_ref", "scanned_at", "created_at", "last_referenced_at", "delete_after", "deleted_at"}
	_, err = s.db.ExecContext(ctx, s.store.InsertStatement("lifecycle_file_artifacts", columns), artifact.ID, artifact.WorkspaceID, artifact.ObjectKey, artifact.FieldKey, artifact.Filename, artifact.ContentType, artifact.SHA256, artifact.Size, "staged", lifecyclecontract.FileScanPending, "", "", "", createdAt, "", artifact.CreatedAt.UTC().Add(uploadArtifactGracePeriod).Format(time.RFC3339Nano), "")
	return err
}

func (s *FileArtifactStore) FindFileScan(ctx context.Context, workspaceID, fileID string) (lifecyclecontract.FileScanEvidence, error) {
	if s == nil || s.store == nil {
		return lifecyclecontract.FileScanEvidence{}, fmt.Errorf("upload artifact store unavailable")
	}
	workspace, err := principalmodel.NewWorkspaceID(workspaceID)
	if err != nil {
		return lifecyclecontract.FileScanEvidence{}, err
	}
	columns := []string{"id", "workspace_id", "filename", "content_type", "object_key", "field_key", "sha256", "size_bytes", "scan_status", "scan_provider", "scan_evidence_ref", "scanned_at"}
	query := "SELECT " + strings.Join(database.QuotedColumns(s.store, columns), ", ") + " FROM " + s.store.TableIdentifier("lifecycle_file_artifacts") + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(1) + " AND " + s.store.Identifier("id") + " = " + s.store.Placeholder(2) + " AND " + s.store.Identifier("deleted_at") + " = ''"
	var evidence lifecyclecontract.FileScanEvidence
	var scannedAt string
	err = s.db.QueryRowContext(ctx, query, workspace.String(), strings.TrimSpace(fileID)).Scan(&evidence.FileID, &evidence.WorkspaceID, &evidence.Filename, &evidence.ContentType, &evidence.ObjectKey, &evidence.FieldKey, &evidence.SHA256, &evidence.Size, &evidence.Status, &evidence.Provider, &evidence.EvidenceRef, &scannedAt)
	if err != nil {
		return lifecyclecontract.FileScanEvidence{}, err
	}
	if scannedAt != "" {
		evidence.ScannedAt, err = time.Parse(time.RFC3339Nano, scannedAt)
	}
	return evidence, err
}

// RecordFileScan is an internal scanner port. No HTTP or generated Action
// binding exposes it; only trusted Runtime scanner adapters may call it.
func (s *FileArtifactStore) RecordFileScan(ctx context.Context, evidence lifecyclecontract.FileScanEvidence) error {
	switch evidence.Status {
	case lifecyclecontract.FileScanClean, lifecyclecontract.FileScanQuarantined, lifecyclecontract.FileScanFailed:
	default:
		return fmt.Errorf("terminal file scan status required")
	}
	if strings.TrimSpace(evidence.Provider) == "" || strings.TrimSpace(evidence.EvidenceRef) == "" || evidence.ScannedAt.IsZero() {
		return fmt.Errorf("file scan evidence is incomplete")
	}
	current, err := s.FindFileScan(ctx, evidence.WorkspaceID, evidence.FileID)
	if err != nil {
		return err
	}
	if current.SHA256 != strings.TrimSpace(evidence.SHA256) || current.Size != evidence.Size {
		return fmt.Errorf("file scan content identity mismatch")
	}
	query := "UPDATE " + s.store.TableIdentifier("lifecycle_file_artifacts") + " SET " + s.store.Identifier("scan_status") + " = " + s.store.Placeholder(1) + ", " + s.store.Identifier("scan_provider") + " = " + s.store.Placeholder(2) + ", " + s.store.Identifier("scan_evidence_ref") + " = " + s.store.Placeholder(3) + ", " + s.store.Identifier("scanned_at") + " = " + s.store.Placeholder(4) + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(5) + " AND " + s.store.Identifier("id") + " = " + s.store.Placeholder(6) + " AND " + s.store.Identifier("sha256") + " = " + s.store.Placeholder(7) + " AND " + s.store.Identifier("size_bytes") + " = " + s.store.Placeholder(8)
	result, err := s.db.ExecContext(ctx, query, evidence.Status, strings.TrimSpace(evidence.Provider), strings.TrimSpace(evidence.EvidenceRef), evidence.ScannedAt.UTC().Format(time.RFC3339Nano), current.WorkspaceID, current.FileID, current.SHA256, current.Size)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return fmt.Errorf("file scan content identity mismatch")
	}
	return nil
}

func (s *FileArtifactStore) ReconcileUploadArtifacts(ctx context.Context, now time.Time, limit int) (lifecyclecontract.UploadCleanupResult, error) {
	result := lifecyclecontract.UploadCleanupResult{}
	if s == nil || s.store == nil {
		return result, fmt.Errorf("upload artifact store unavailable")
	}
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	expiredDownloads, err := s.expireDownloadTasks(ctx, now, limit)
	if err != nil {
		return result, err
	}
	result.ExpiredDownloads = expiredDownloads
	columns := []string{"id", "workspace_id", "object_key", "field_key", "filename", "status", "created_at", "delete_after"}
	query := "SELECT " + strings.Join(database.QuotedColumns(s.store, columns), ", ") + " FROM " + s.store.TableIdentifier("lifecycle_file_artifacts") + " WHERE " + s.store.Identifier("status") + " <> 'deleted' ORDER BY " + s.store.Identifier("created_at") + ", " + s.store.Identifier("id") + " LIMIT " + s.store.Placeholder(1)
	rows, err := s.db.QueryContext(ctx, query, limit)
	if err != nil {
		return result, err
	}
	type candidate struct{ id, workspaceID, objectKey, fieldKey, filename, status, createdAt, deleteAfter string }
	candidates := []candidate{}
	for rows.Next() {
		var item candidate
		if err := rows.Scan(&item.id, &item.workspaceID, &item.objectKey, &item.fieldKey, &item.filename, &item.status, &item.createdAt, &item.deleteAfter); err != nil {
			_ = rows.Close()
			return result, err
		}
		candidates = append(candidates, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return result, err
	}
	_ = rows.Close()
	for _, item := range candidates {
		if err := s.contextErr(ctx); err != nil {
			return result, err
		}
		result.Scanned++
		referenced, err := s.artifactReferenced(ctx, item.workspaceID, item.objectKey, item.fieldKey, item.filename)
		if err != nil {
			return result, err
		}
		if referenced {
			if err := s.updateArtifactState(ctx, item.workspaceID, item.id, "referenced", now, time.Time{}); err != nil {
				return result, err
			}
			result.Referenced++
			continue
		}
		deleteAfter, _ := time.Parse(time.RFC3339Nano, item.deleteAfter)
		if item.status == "referenced" || deleteAfter.IsZero() {
			if err := s.updateArtifactState(ctx, item.workspaceID, item.id, "orphaned", time.Time{}, now.Add(uploadArtifactGracePeriod)); err != nil {
				return result, err
			}
			result.Orphaned++
			continue
		}
		if now.Before(deleteAfter) {
			continue
		}
		path, err := s.artifactPath(item.workspaceID, item.filename)
		if err != nil {
			return result, err
		}
		if err := s.removeFile(path); err != nil && !os.IsNotExist(err) {
			return result, err
		}
		if err := s.markArtifactDeleted(ctx, item.workspaceID, item.id, now); err != nil {
			return result, err
		}
		result.Deleted++
	}
	return result, nil
}

func (s *FileArtifactStore) expireDownloadTasks(ctx context.Context, now time.Time, limit int) (int, error) {
	object, ok := s.objects["download_task"]
	if !ok || !lifecycleObjectHasField(object, "token_status") || !lifecycleObjectHasField(object, "file_name") || !lifecycleObjectHasField(object, "status") {
		return 0, nil
	}
	cutoff := now.Add(-7 * 24 * time.Hour).UTC().Format(time.RFC3339Nano)
	query := "SELECT " + s.store.Identifier("workspace_id") + ", " + s.store.Identifier("id") + " FROM " + s.store.TableIdentifier("download_task") + " WHERE " + s.store.Identifier("token_status") + " = 'active' AND " + s.store.Identifier("updated_at") + " <= " + s.store.Placeholder(1) + " ORDER BY " + s.store.Identifier("updated_at") + ", " + s.store.Identifier("id") + " LIMIT " + s.store.Placeholder(2)
	rows, err := s.db.QueryContext(ctx, query, cutoff, limit)
	if err != nil {
		return 0, err
	}
	type identity struct{ workspaceID, id string }
	identities := []identity{}
	for rows.Next() {
		var item identity
		if err := rows.Scan(&item.workspaceID, &item.id); err != nil {
			_ = rows.Close()
			return 0, err
		}
		identities = append(identities, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, err
	}
	_ = rows.Close()
	expired := 0
	for _, item := range identities {
		update := "UPDATE " + s.store.TableIdentifier("download_task") + " SET " + s.store.Identifier("token_status") + " = 'expired', " + s.store.Identifier("status") + " = 'expired', " + s.store.Identifier("file_name") + " = '', " + s.store.Identifier("updated_at") + " = " + s.store.Placeholder(1) + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(2) + " AND " + s.store.Identifier("id") + " = " + s.store.Placeholder(3) + " AND " + s.store.Identifier("token_status") + " = 'active'"
		changed, err := s.db.ExecContext(ctx, update, now.UTC().Format(time.RFC3339Nano), item.workspaceID, item.id)
		if err != nil {
			return expired, err
		}
		count, err := changed.RowsAffected()
		if err != nil {
			return expired, fmt.Errorf("read expired download task count: %w", err)
		}
		expired += int(count)
	}
	return expired, nil
}

func (s *FileArtifactStore) artifactReferenced(ctx context.Context, workspaceID, objectKey, fieldKey, filename string) (bool, error) {
	object, ok := s.objects[objectKey]
	if !ok || !lifecycleObjectHasField(object, fieldKey) {
		return false, nil
	}
	reference := "/uploads/" + filename
	query := "SELECT COUNT(*) FROM " + s.store.TableIdentifier(objectKey) + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(1) + " AND (" + s.store.Identifier(fieldKey) + " = " + s.store.Placeholder(2) + " OR " + s.store.Identifier(fieldKey) + " LIKE " + s.store.Placeholder(3) + ")"
	var count int
	if err := s.db.QueryRowContext(ctx, query, workspaceID, reference, "%\""+reference+"\"%").Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s *FileArtifactStore) updateArtifactState(ctx context.Context, workspaceID, id, status string, referencedAt, deleteAfter time.Time) error {
	lastReferenced, deletion := "", ""
	if !referencedAt.IsZero() {
		lastReferenced = referencedAt.UTC().Format(time.RFC3339Nano)
	}
	if !deleteAfter.IsZero() {
		deletion = deleteAfter.UTC().Format(time.RFC3339Nano)
	}
	query := "UPDATE " + s.store.TableIdentifier("lifecycle_file_artifacts") + " SET " + s.store.Identifier("status") + " = " + s.store.Placeholder(1) + ", " + s.store.Identifier("last_referenced_at") + " = CASE WHEN " + s.store.Placeholder(2) + " = '' THEN " + s.store.Identifier("last_referenced_at") + " ELSE " + s.store.Placeholder(3) + " END, " + s.store.Identifier("delete_after") + " = " + s.store.Placeholder(4) + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(5) + " AND " + s.store.Identifier("id") + " = " + s.store.Placeholder(6)
	_, err := s.db.ExecContext(ctx, query, status, lastReferenced, lastReferenced, deletion, workspaceID, id)
	return err
}

func (s *FileArtifactStore) markArtifactDeleted(ctx context.Context, workspaceID, id string, now time.Time) error {
	query := "UPDATE " + s.store.TableIdentifier("lifecycle_file_artifacts") + " SET " + s.store.Identifier("status") + " = 'deleted', " + s.store.Identifier("delete_after") + " = '', " + s.store.Identifier("deleted_at") + " = " + s.store.Placeholder(1) + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(2) + " AND " + s.store.Identifier("id") + " = " + s.store.Placeholder(3)
	_, err := s.db.ExecContext(ctx, query, now.UTC().Format(time.RFC3339Nano), workspaceID, id)
	return err
}

func (s *FileArtifactStore) artifactPath(workspaceID, filename string) (string, error) {
	workspace, err := principalmodel.NewWorkspaceID(workspaceID)
	if err != nil {
		return "", err
	}
	if filename == "" || filepath.Base(filename) != filename {
		return "", fmt.Errorf("invalid upload artifact filename")
	}
	root, err := s.absPath(strings.TrimSpace(s.uploadRoot))
	if err != nil || strings.TrimSpace(s.uploadRoot) == "" {
		return "", fmt.Errorf("upload root is required")
	}
	digest := sha256.Sum256([]byte(workspace.String()))
	path := filepath.Join(root, "workspace-"+hex.EncodeToString(digest[:16]), filename)
	relative, err := s.relPath(root, path)
	if err != nil || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("upload artifact path escapes root")
	}
	return path, nil
}

func lifecycleObjectHasField(object definitionmodel.ObjectSchema, fieldKey string) bool {
	for _, field := range object.Fields {
		if strings.TrimSpace(field.Key) == fieldKey {
			return true
		}
	}
	return false
}
