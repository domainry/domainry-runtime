package blobstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/domainry/domainry-runtime/pkg/runtimefile"
)

var localBlobKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,254}$`)
var localStageKeyPattern = regexp.MustCompile(`^stage-[0-9a-f]{32}$`)
var localContentSHA256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type LocalStore struct{ root string }

func NewLocalStore(root string) (*LocalStore, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, fmt.Errorf("local blob root is required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve local blob root: %w", err)
	}
	return &LocalStore{root: absolute}, nil
}

func (*LocalStore) Descriptor() runtimefile.AdapterDescriptor {
	return runtimefile.AdapterDescriptor{Provider: "domainry-local-filesystem", Revision: "v1"}
}

func (s *LocalStore) Stage(ctx context.Context, request runtimefile.BlobStageRequest) (runtimefile.BlobInfo, error) {
	if err := ctx.Err(); err != nil {
		return runtimefile.BlobInfo{}, err
	}
	if strings.TrimSpace(request.WorkspaceID) == "" || strings.TrimSpace(request.StageID) == "" || request.Content == nil || request.MaxBytes < 1 {
		return runtimefile.BlobInfo{}, fmt.Errorf("local blob stage request is invalid")
	}
	workspaceID := strings.TrimSpace(request.WorkspaceID)
	directory := s.workspaceDirectory(workspaceID)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return runtimefile.BlobInfo{}, err
	}
	stageDigest := sha256.Sum256([]byte(strings.TrimSpace(request.StageID)))
	stageKey := "stage-" + hex.EncodeToString(stageDigest[:16])
	root, err := os.OpenRoot(directory)
	if err != nil {
		return runtimefile.BlobInfo{}, err
	}
	defer root.Close()
	file, err := root.OpenFile(stageKey, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return runtimefile.BlobInfo{}, runtimefile.ErrBlobIdentityConflict
		}
		return runtimefile.BlobInfo{}, err
	}
	keep := false
	defer func() {
		_ = file.Close()
		if !keep {
			_ = root.Remove(stageKey)
		}
	}()
	hash := sha256.New()
	limited := &io.LimitedReader{R: contextReader{ctx: ctx, reader: request.Content}, N: request.MaxBytes}
	size, err := io.Copy(io.MultiWriter(file, hash), limited)
	if err != nil {
		return runtimefile.BlobInfo{}, err
	}
	if limited.N == 0 {
		var extra [1]byte
		count, readErr := contextReader{ctx: ctx, reader: request.Content}.Read(extra[:])
		if count > 0 {
			return runtimefile.BlobInfo{}, runtimefile.ErrBlobTooLarge
		}
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return runtimefile.BlobInfo{}, readErr
		}
	}
	if err := file.Sync(); err != nil {
		return runtimefile.BlobInfo{}, err
	}
	if err := file.Close(); err != nil {
		return runtimefile.BlobInfo{}, err
	}
	keep = true
	return runtimefile.BlobInfo{WorkspaceID: workspaceID, BlobKey: stageKey, ContentSHA256: hex.EncodeToString(hash.Sum(nil)), Size: size}, nil
}

func (s *LocalStore) Commit(ctx context.Context, request runtimefile.BlobCommitRequest) (runtimefile.BlobInfo, error) {
	if err := ctx.Err(); err != nil {
		return runtimefile.BlobInfo{}, err
	}
	request.WorkspaceID, request.StageKey, request.BlobKey, request.ContentSHA256 = strings.TrimSpace(request.WorkspaceID), strings.TrimSpace(request.StageKey), strings.TrimSpace(request.BlobKey), strings.ToLower(strings.TrimSpace(request.ContentSHA256))
	if request.WorkspaceID == "" || !localStageKeyPattern.MatchString(request.StageKey) || !validLocalBlobKey(request.BlobKey) || !localContentSHA256Pattern.MatchString(request.ContentSHA256) || request.Size < 0 || !strings.Contains(request.BlobKey, request.ContentSHA256) {
		return runtimefile.BlobInfo{}, fmt.Errorf("local blob commit request is invalid")
	}
	staged, err := s.Stat(ctx, request.WorkspaceID, request.StageKey)
	if err != nil {
		if errors.Is(err, runtimefile.ErrBlobNotFound) {
			existing, existingErr := s.Stat(ctx, request.WorkspaceID, request.BlobKey)
			if existingErr == nil && existing.Size == request.Size && strings.EqualFold(existing.ContentSHA256, request.ContentSHA256) {
				return existing, nil
			}
			if existingErr == nil || errors.Is(existingErr, runtimefile.ErrBlobNotFound) {
				return runtimefile.BlobInfo{}, runtimefile.ErrBlobIdentityConflict
			}
			return runtimefile.BlobInfo{}, existingErr
		}
		return runtimefile.BlobInfo{}, err
	}
	if staged.Size != request.Size || !strings.EqualFold(staged.ContentSHA256, request.ContentSHA256) {
		return runtimefile.BlobInfo{}, runtimefile.ErrBlobIdentityConflict
	}
	root, err := os.OpenRoot(s.workspaceDirectory(request.WorkspaceID))
	if err != nil {
		return runtimefile.BlobInfo{}, err
	}
	defer root.Close()
	if err := root.Link(request.StageKey, request.BlobKey); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return runtimefile.BlobInfo{}, err
		}
		existing, statErr := s.Stat(ctx, request.WorkspaceID, request.BlobKey)
		if statErr != nil || existing.Size != request.Size || !strings.EqualFold(existing.ContentSHA256, request.ContentSHA256) {
			return runtimefile.BlobInfo{}, runtimefile.ErrBlobIdentityConflict
		}
	}
	if err := root.Remove(request.StageKey); err != nil && !errors.Is(err, os.ErrNotExist) {
		return runtimefile.BlobInfo{}, err
	}
	return s.Stat(ctx, request.WorkspaceID, request.BlobKey)
}

func (s *LocalStore) Open(ctx context.Context, workspaceID, blobKey string) (io.ReadCloser, error) {
	return s.openFile(ctx, workspaceID, blobKey)
}

func (s *LocalStore) openFile(ctx context.Context, workspaceID, blobKey string) (*os.File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(workspaceID) == "" || !validLocalBlobKey(blobKey) {
		return nil, fmt.Errorf("local blob identity is invalid")
	}
	root, err := os.OpenRoot(s.workspaceDirectory(workspaceID))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, runtimefile.ErrBlobNotFound
		}
		return nil, err
	}
	defer root.Close()
	info, err := root.Lstat(strings.TrimSpace(blobKey))
	if errors.Is(err, os.ErrNotExist) {
		return nil, runtimefile.ErrBlobNotFound
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, runtimefile.ErrBlobIdentityConflict
	}
	file, err := root.Open(strings.TrimSpace(blobKey))
	if errors.Is(err, os.ErrNotExist) {
		return nil, runtimefile.ErrBlobNotFound
	}
	return file, err
}

func (s *LocalStore) Stat(ctx context.Context, workspaceID, blobKey string) (runtimefile.BlobInfo, error) {
	reader, err := s.openFile(ctx, workspaceID, blobKey)
	if err != nil {
		return runtimefile.BlobInfo{}, err
	}
	defer reader.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, reader)
	if err != nil {
		return runtimefile.BlobInfo{}, err
	}
	info, err := reader.Stat()
	if err != nil {
		return runtimefile.BlobInfo{}, err
	}
	return runtimefile.BlobInfo{WorkspaceID: strings.TrimSpace(workspaceID), BlobKey: strings.TrimSpace(blobKey), ContentSHA256: hex.EncodeToString(hash.Sum(nil)), Size: size, ModifiedAt: info.ModTime().UTC()}, nil
}

func (s *LocalStore) Delete(ctx context.Context, workspaceID, blobKey string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(workspaceID) == "" || !validLocalBlobKey(blobKey) {
		return fmt.Errorf("local blob identity is invalid")
	}
	root, err := os.OpenRoot(s.workspaceDirectory(workspaceID))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.Remove(strings.TrimSpace(blobKey)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s *LocalStore) workspaceDirectory(workspaceID string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(workspaceID)))
	return filepath.Join(s.root, "workspace-"+hex.EncodeToString(digest[:16]))
}

func (s *LocalStore) path(workspaceID, blobKey string) (string, error) {
	if strings.TrimSpace(workspaceID) == "" || !validLocalBlobKey(blobKey) {
		return "", fmt.Errorf("local blob identity is invalid")
	}
	return filepath.Join(s.workspaceDirectory(workspaceID), strings.TrimSpace(blobKey)), nil
}

func validLocalBlobKey(value string) bool {
	value = strings.TrimSpace(value)
	return localBlobKeyPattern.MatchString(value) && filepath.Base(value) == value && !strings.Contains(value, "..")
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(content []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(content)
}

var _ runtimefile.BlobStore = (*LocalStore)(nil)
