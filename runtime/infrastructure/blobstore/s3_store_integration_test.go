package blobstore

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	"github.com/domainry/domainry-runtime/pkg/runtimefile"
)

func TestS3StoreAgainstConfiguredEndpoint(t *testing.T) {
	endpoint := os.Getenv("RUNTIME_S3_INTEGRATION_ENDPOINT")
	if endpoint == "" {
		t.Skip("RUNTIME_S3_INTEGRATION_ENDPOINT is not configured")
	}
	store, err := NewS3Store(t.Context(), S3StoreConfig{
		Region:                      integrationValue("RUNTIME_S3_INTEGRATION_REGION", "us-east-1"),
		Bucket:                      integrationValue("RUNTIME_S3_INTEGRATION_BUCKET", "kb-raw"),
		Prefix:                      "integration/domainry-runtime",
		Endpoint:                    endpoint,
		ForcePathStyle:              true,
		AllowInsecureEndpoint:       true,
		DisableServerSideEncryption: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("domainry-runtime-s3-integration")
	staged, err := store.Stage(t.Context(), runtimefile.BlobStageRequest{
		WorkspaceID: "integration-workspace",
		StageID:     fmt.Sprintf("%s-%d", t.Name(), time.Now().UnixNano()),
		Content:     bytes.NewReader(content),
		MaxBytes:    int64(len(content)),
	})
	if err != nil {
		t.Fatal(err)
	}
	blobKey := staged.ContentSHA256 + "-integration.bin"
	defer func() { _ = store.Delete(t.Context(), "integration-workspace", staged.BlobKey) }()
	defer func() { _ = store.Delete(t.Context(), "integration-workspace", blobKey) }()
	committed, err := store.Commit(t.Context(), runtimefile.BlobCommitRequest{
		WorkspaceID:   "integration-workspace",
		StageKey:      staged.BlobKey,
		BlobKey:       blobKey,
		ContentSHA256: staged.ContentSHA256,
		Size:          staged.Size,
	})
	if err != nil {
		t.Fatal(err)
	}
	if committed.Size != int64(len(content)) || committed.ContentSHA256 != staged.ContentSHA256 {
		t.Fatalf("committed=%+v staged=%+v", committed, staged)
	}
	reader, err := store.Open(t.Context(), "integration-workspace", blobKey)
	if err != nil {
		t.Fatal(err)
	}
	loaded, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil || !bytes.Equal(loaded, content) {
		t.Fatalf("loaded=%q readErr=%v closeErr=%v", loaded, readErr, closeErr)
	}
	if err = store.Delete(t.Context(), "integration-workspace", blobKey); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Stat(t.Context(), "integration-workspace", blobKey); !errors.Is(err, runtimefile.ErrBlobNotFound) {
		t.Fatalf("deleted blob stat err=%v", err)
	}
}

func integrationValue(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
