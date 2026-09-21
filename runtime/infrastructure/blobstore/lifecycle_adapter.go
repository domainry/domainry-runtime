package blobstore

import (
	"context"
	"errors"
	"io"

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
	return lifecyclecontract.ArtifactContentInfo{SHA256: info.ContentSHA256, Size: info.Size}, nil
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
