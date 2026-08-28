package filesystem

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	requestcontext "github.com/domainry/domainry-foundation/requestcontext"
	lifecyclecontract "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/contract"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

const lifecycleSubjectFileLimit = 5 << 20

type SubjectStore struct {
	uploadRoot string
	directory  string
}

type lifecycleSubjectArtifact struct {
	WorkspaceID string          `json:"workspace_id"`
	ExpiresAt   time.Time       `json:"expires_at"`
	Payload     json.RawMessage `json:"payload"`
}

func NewSubjectStore(directory string) *SubjectStore {
	return &SubjectStore{uploadRoot: directory, directory: filepath.Join(directory, "lifecycle-exports")}
}

func (s *SubjectStore) PutSubjectExport(ctx context.Context, workspaceID, requestID string, payload json.RawMessage, expiresAt time.Time) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(requestID) == "" || !expiresAt.After(time.Now().UTC()) {
		return "", fmt.Errorf("subject export scope and future expiry required")
	}
	if err := localMkdirAll(s.directory, 0o700); err != nil {
		return "", err
	}
	reference := requestID + "-" + requestcontext.NewRequestID()
	raw, err := json.Marshal(lifecycleSubjectArtifact{WorkspaceID: workspaceID, ExpiresAt: expiresAt, Payload: payload})
	if err != nil {
		return "", err
	}
	temporary, err := localCreateTemp(s.directory, ".lifecycle-export-*")
	if err != nil {
		return "", err
	}
	temporaryName := temporary.Name()
	defer localRemove(temporaryName)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return "", err
	}
	if _, err := temporary.Write(raw); err != nil {
		_ = temporary.Close()
		return "", err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return "", err
	}
	if err := temporary.Close(); err != nil {
		return "", err
	}
	if err := localRename(temporaryName, filepath.Join(s.directory, reference+".json")); err != nil {
		return "", err
	}
	return reference, nil
}

func (s *SubjectStore) ReadSubjectExport(ctx context.Context, workspaceID, reference string, now time.Time) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := s.path(reference)
	if err != nil {
		return nil, err
	}
	raw, err := localReadFile(path)
	if err != nil {
		return nil, err
	}
	var artifact lifecycleSubjectArtifact
	if err := json.Unmarshal(raw, &artifact); err != nil {
		return nil, err
	}
	if artifact.WorkspaceID != workspaceID || !now.Before(artifact.ExpiresAt) {
		return nil, fmt.Errorf("subject export unavailable or expired")
	}
	return artifact.Payload, nil
}

func (s *SubjectStore) DeleteExpiredSubjectExports(ctx context.Context, now time.Time) (int, error) {
	entries, err := localReadDir(s.directory)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	deleted := 0
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return deleted, err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(s.directory, entry.Name())
		raw, readErr := localReadFile(path)
		var artifact lifecycleSubjectArtifact
		if readErr != nil || json.Unmarshal(raw, &artifact) != nil || now.Before(artifact.ExpiresAt) {
			continue
		}
		if removeErr := localRemove(path); removeErr != nil {
			return deleted, removeErr
		}
		deleted++
	}
	return deleted, nil
}

// DeleteExpiredUploadStaging removes only interrupted upload temporary files.
// Final content-addressed files are deliberately excluded until the upload
// owner has a durable reference registry capable of proving orphan status.
func (s *SubjectStore) DeleteExpiredUploadStaging(ctx context.Context, now time.Time) (int, error) {
	if strings.TrimSpace(s.uploadRoot) == "" {
		return 0, fmt.Errorf("upload root is required")
	}
	root, err := localAbsPath(strings.TrimSpace(s.uploadRoot))
	if err != nil {
		return 0, fmt.Errorf("upload root is required")
	}
	entries, err := localReadDir(root)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	cutoff := now.Add(-time.Hour)
	deleted := 0
	for _, workspace := range entries {
		if err := ctx.Err(); err != nil {
			return deleted, err
		}
		if !workspace.IsDir() || !strings.HasPrefix(workspace.Name(), "workspace-") {
			continue
		}
		workspacePath := filepath.Join(root, workspace.Name())
		files, readErr := localReadDir(workspacePath)
		if readErr != nil {
			return deleted, readErr
		}
		for _, file := range files {
			if file.IsDir() || !strings.HasPrefix(file.Name(), ".upload-") {
				continue
			}
			info, infoErr := localDirEntryInfo(file)
			if infoErr != nil {
				return deleted, infoErr
			}
			if info.ModTime().After(cutoff) {
				continue
			}
			if removeErr := localRemove(filepath.Join(workspacePath, file.Name())); removeErr != nil {
				return deleted, removeErr
			}
			deleted++
		}
	}
	return deleted, nil
}

func (s *SubjectStore) ExportSubjectFile(ctx context.Context, reference lifecyclecontract.SubjectFileReference) (lifecyclecontract.SubjectFileEvidence, error) {
	path, filename, err := s.subjectFilePath(reference)
	if err != nil {
		return lifecyclecontract.SubjectFileEvidence{}, err
	}
	if err := ctx.Err(); err != nil {
		return lifecyclecontract.SubjectFileEvidence{}, err
	}
	file, err := localOpenFile(path)
	if err != nil {
		return lifecyclecontract.SubjectFileEvidence{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return lifecyclecontract.SubjectFileEvidence{}, err
	}
	if info.Size() > lifecycleSubjectFileLimit {
		return lifecyclecontract.SubjectFileEvidence{}, fmt.Errorf("subject file exceeds governed upload limit")
	}
	content, err := localReadFile(path)
	if err != nil {
		return lifecyclecontract.SubjectFileEvidence{}, err
	}
	digest := sha256.Sum256(content)
	return lifecyclecontract.SubjectFileEvidence{Reference: reference.Reference, Filename: filename, ContentType: http.DetectContentType(content), Size: int64(len(content)), SHA256: hex.EncodeToString(digest[:]), Content: content}, nil
}

func (s *SubjectStore) DeleteSubjectFile(ctx context.Context, reference lifecyclecontract.SubjectFileReference) (lifecyclecontract.SubjectFileEvidence, error) {
	path, _, err := s.subjectFilePath(reference)
	if err != nil {
		return lifecyclecontract.SubjectFileEvidence{}, err
	}
	evidence, err := s.ExportSubjectFile(ctx, reference)
	if err != nil {
		return lifecyclecontract.SubjectFileEvidence{}, err
	}
	evidence.Content = nil
	if err := localRemove(path); err != nil {
		return lifecyclecontract.SubjectFileEvidence{}, err
	}
	return evidence, nil
}

func (s *SubjectStore) subjectFilePath(reference lifecyclecontract.SubjectFileReference) (string, string, error) {
	workspace, err := principalmodel.NewWorkspaceID(reference.WorkspaceID)
	if err != nil {
		return "", "", err
	}
	parsed, err := url.Parse(strings.TrimSpace(reference.Reference))
	if err != nil {
		return "", "", err
	}
	filename := filepath.Base(parsed.Path)
	if filename == "." || filename == ".." || filename == string(filepath.Separator) {
		return "", "", fmt.Errorf("invalid subject file reference")
	}
	digest := sha256.Sum256([]byte(workspace.String()))
	workspaceDirectory := "workspace-" + hex.EncodeToString(digest[:16])
	if strings.TrimSpace(s.uploadRoot) == "" {
		return "", "", fmt.Errorf("upload root is required")
	}
	root, err := localAbsPath(strings.TrimSpace(s.uploadRoot))
	if err != nil {
		return "", "", fmt.Errorf("upload root is required")
	}
	path := filepath.Join(root, workspaceDirectory, filename)
	return path, filename, nil
}

func (s *SubjectStore) path(reference string) (string, error) {
	reference = strings.TrimSpace(reference)
	if reference == "" || filepath.Base(reference) != reference || strings.Contains(reference, "..") {
		return "", fmt.Errorf("invalid subject export reference")
	}
	return filepath.Join(s.directory, reference+".json"), nil
}
