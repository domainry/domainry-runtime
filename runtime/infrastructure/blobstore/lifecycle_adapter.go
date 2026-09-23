package blobstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"

	requestcontext "github.com/domainry/domainry-foundation/requestcontext"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	"github.com/domainry/domainry-runtime/pkg/runtimefile"
)

// LifecycleContentStore narrows Runtime's deployment BlobStore to the neutral
// byte contract consumed by Lifecycle retention and subject erasure.
type LifecycleContentStore struct{ Blobs runtimefile.BlobStore }

func (s LifecycleContentStore) Open(ctx context.Context, workspaceID, blobKey string) (io.ReadCloser, error) {
	reader, err := s.Blobs.Open(ctx, workspaceID, blobKey)
	return reader, lifecycleContentError(err)
}

func (s LifecycleContentStore) Stat(ctx context.Context, workspaceID, blobKey string) (lifecyclecontract.ArtifactContentInfo, error) {
	info, err := s.Blobs.Stat(ctx, workspaceID, blobKey)
	if err != nil {
		return lifecyclecontract.ArtifactContentInfo{}, lifecycleContentError(err)
	}
	return lifecyclecontract.ArtifactContentInfo{Reference: info.BlobKey, SHA256: info.ContentSHA256, Size: info.Size}, nil
}

func (s LifecycleContentStore) PutImmutable(ctx context.Context, workspaceID, identity string, content []byte) (lifecyclecontract.ArtifactContentInfo, error) {
	if s.Blobs == nil || strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(identity) == "" {
		return lifecyclecontract.ArtifactContentInfo{}, errors.New("lifecycle artifact content writer is unavailable")
	}
	digest := sha256.Sum256(content)
	digestText := hex.EncodeToString(digest[:])
	stage, err := s.Blobs.Stage(ctx, runtimefile.BlobStageRequest{WorkspaceID: workspaceID, StageID: identity + ":" + requestcontext.NewRequestID(), Content: bytes.NewReader(content), MaxBytes: int64(len(content)) + 1})
	if err != nil {
		return lifecyclecontract.ArtifactContentInfo{}, err
	}
	key := lifecycleBlobKey(identity, digestText)
	info, err := s.Blobs.Commit(ctx, runtimefile.BlobCommitRequest{WorkspaceID: workspaceID, StageKey: stage.BlobKey, BlobKey: key, ContentSHA256: stage.ContentSHA256, Size: stage.Size})
	if err != nil {
		return lifecyclecontract.ArtifactContentInfo{}, lifecycleContentError(err)
	}
	return lifecyclecontract.ArtifactContentInfo{Reference: info.BlobKey, SHA256: info.ContentSHA256, Size: info.Size}, nil
}

func lifecycleBlobKey(identity, digest string) string {
	identityDigest := sha256.Sum256([]byte(strings.TrimSpace(identity)))
	return "lifecycle-" + hex.EncodeToString(identityDigest[:8]) + "-" + strings.ToLower(strings.TrimSpace(digest))
}

func (s LifecycleContentStore) Delete(ctx context.Context, workspaceID, blobKey string) error {
	return lifecycleContentError(s.Blobs.Delete(ctx, workspaceID, blobKey))
}

func lifecycleContentError(err error) error {
	if errors.Is(err, runtimefile.ErrBlobNotFound) {
		return lifecyclecontract.ErrArtifactContentNotFound
	}
	return err
}

var _ lifecyclecontract.ArtifactContentStore = LifecycleContentStore{}
var _ lifecyclecontract.ArtifactContentWriter = LifecycleContentStore{}
