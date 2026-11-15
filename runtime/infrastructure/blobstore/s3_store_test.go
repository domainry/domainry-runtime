package blobstore

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/domainry/domainry-runtime/pkg/runtimefile"
)

type fakeS3BlobObject struct {
	content  []byte
	metadata map[string]string
	modified time.Time
}

type fakeS3BlobClient struct {
	mu      sync.Mutex
	objects map[string]fakeS3BlobObject
}

func (client *fakeS3BlobClient) PutObject(_ context.Context, input *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	content, err := io.ReadAll(input.Body)
	if err != nil {
		return nil, err
	}
	key := aws.ToString(input.Key)
	client.mu.Lock()
	defer client.mu.Unlock()
	if aws.ToString(input.IfNoneMatch) == "*" {
		if _, found := client.objects[key]; found {
			return nil, &smithy.GenericAPIError{Code: "PreconditionFailed", Message: "object exists"}
		}
	}
	metadata := make(map[string]string, len(input.Metadata))
	for name, value := range input.Metadata {
		metadata[name] = value
	}
	client.objects[key] = fakeS3BlobObject{content: content, metadata: metadata, modified: time.Now().UTC()}
	return &s3.PutObjectOutput{}, nil
}

func (client *fakeS3BlobClient) GetObject(_ context.Context, input *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	object, found := client.objects[aws.ToString(input.Key)]
	if !found {
		return nil, &smithy.GenericAPIError{Code: "NoSuchKey", Message: "missing"}
	}
	content := append([]byte(nil), object.content...)
	return &s3.GetObjectOutput{
		Body: io.NopCloser(bytes.NewReader(content)), ContentLength: aws.Int64(int64(len(content))),
		Metadata: object.metadata, LastModified: aws.Time(object.modified),
	}, nil
}

func (client *fakeS3BlobClient) HeadObject(_ context.Context, input *s3.HeadObjectInput, _ ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	object, found := client.objects[aws.ToString(input.Key)]
	if !found {
		return nil, &smithy.GenericAPIError{Code: "NotFound", Message: "missing"}
	}
	return &s3.HeadObjectOutput{ContentLength: aws.Int64(int64(len(object.content))), Metadata: object.metadata, LastModified: aws.Time(object.modified)}, nil
}

func (client *fakeS3BlobClient) DeleteObject(_ context.Context, input *s3.DeleteObjectInput, _ ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	delete(client.objects, aws.ToString(input.Key))
	return &s3.DeleteObjectOutput{}, nil
}

func newFakeS3Store(t *testing.T) (*S3Store, *fakeS3BlobClient) {
	t.Helper()
	client := &fakeS3BlobClient{objects: map[string]fakeS3BlobObject{}}
	store, err := newS3Store(S3StoreConfig{
		Region: "us-east-1", Bucket: "runtime", Prefix: "files",
		Endpoint: "http://127.0.0.1:9000", ForcePathStyle: true,
	}, client)
	if err != nil {
		t.Fatal(err)
	}
	return store, client
}

func TestS3StoreStageCommitOpenStatDelete(t *testing.T) {
	store, client := newFakeS3Store(t)
	content := []byte("immutable-runtime-content")
	staged, err := store.Stage(t.Context(), runtimefile.BlobStageRequest{
		WorkspaceID: "workspace-a", StageID: "upload-1", Content: bytes.NewReader(content), MaxBytes: int64(len(content)),
	})
	if err != nil {
		t.Fatal(err)
	}
	key := staged.ContentSHA256 + "-upload-1.bin"
	committed, err := store.Commit(t.Context(), runtimefile.BlobCommitRequest{
		WorkspaceID: "workspace-a", StageKey: staged.BlobKey, BlobKey: key,
		ContentSHA256: staged.ContentSHA256, Size: staged.Size,
	})
	if err != nil {
		t.Fatal(err)
	}
	if committed.BlobKey != key || committed.ContentSHA256 != staged.ContentSHA256 || committed.Size != int64(len(content)) {
		t.Fatalf("committed=%+v staged=%+v", committed, staged)
	}
	reader, err := store.Open(t.Context(), "workspace-a", key)
	if err != nil {
		t.Fatal(err)
	}
	loaded, readErr := io.ReadAll(reader)
	_ = reader.Close()
	if readErr != nil || !bytes.Equal(loaded, content) {
		t.Fatalf("loaded=%q err=%v", loaded, readErr)
	}
	if _, err = store.Stat(t.Context(), "workspace-b", key); !errors.Is(err, runtimefile.ErrBlobNotFound) {
		t.Fatalf("cross-workspace stat err=%v", err)
	}
	client.mu.Lock()
	for objectKey := range client.objects {
		if strings.Contains(objectKey, "workspace-a") {
			t.Fatalf("raw workspace identity leaked into object key %q", objectKey)
		}
	}
	client.mu.Unlock()
	if err = store.Delete(t.Context(), "workspace-a", key); err != nil {
		t.Fatal(err)
	}
	if err = store.Delete(t.Context(), "workspace-a", key); err != nil {
		t.Fatalf("idempotent delete err=%v", err)
	}
	if _, err = store.Open(t.Context(), "workspace-a", key); !errors.Is(err, runtimefile.ErrBlobNotFound) {
		t.Fatalf("deleted open err=%v", err)
	}
}

func TestS3StoreCommitIsIdempotentAndRejectsDifferentIdentity(t *testing.T) {
	store, client := newFakeS3Store(t)
	first := stageS3ForTest(t, store, "stage-1", "same")
	key := first.ContentSHA256 + "-same.bin"
	request := runtimefile.BlobCommitRequest{
		WorkspaceID: "workspace-a", StageKey: first.BlobKey, BlobKey: key,
		ContentSHA256: first.ContentSHA256, Size: first.Size,
	}
	if _, err := store.Commit(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Commit(t.Context(), request); err != nil {
		t.Fatalf("commit replay err=%v", err)
	}
	second := stageS3ForTest(t, store, "stage-2", "same")
	request.StageKey = second.BlobKey
	if _, err := store.Commit(t.Context(), request); err != nil {
		t.Fatalf("identical destination commit err=%v", err)
	}
	client.mu.Lock()
	destinationKey := store.blobObjectKey("workspace-a", key)
	client.objects[destinationKey] = fakeS3BlobObject{
		content:  []byte("tampered"),
		metadata: s3BlobMetadata(strings.Repeat("f", 64), int64(len("tampered"))),
		modified: time.Now().UTC(),
	}
	client.mu.Unlock()
	conflict := stageS3ForTest(t, store, "stage-3", "same")
	conflictRequest := request
	conflictRequest.StageKey = conflict.BlobKey
	if _, err := store.Commit(t.Context(), conflictRequest); !errors.Is(err, runtimefile.ErrBlobIdentityConflict) {
		t.Fatalf("tampered destination identity err=%v", err)
	}
	client.mu.Lock()
	remaining := client.objects[destinationKey]
	client.mu.Unlock()
	if string(remaining.content) != "tampered" {
		t.Fatalf("immutable destination was overwritten: %q", remaining.content)
	}
}

func TestS3StoreRejectsOversizeStageAndUnsafeConfiguration(t *testing.T) {
	store, _ := newFakeS3Store(t)
	if store.serverSideEncryption != types.ServerSideEncryptionAes256 {
		t.Fatalf("default server-side encryption=%q", store.serverSideEncryption)
	}
	if _, err := store.Stage(t.Context(), runtimefile.BlobStageRequest{
		WorkspaceID: "workspace-a", StageID: "oversize", Content: strings.NewReader("12345"), MaxBytes: 4,
	}); !errors.Is(err, runtimefile.ErrBlobTooLarge) {
		t.Fatalf("oversize err=%v", err)
	}
	client := &fakeS3BlobClient{objects: map[string]fakeS3BlobObject{}}
	if _, err := newS3Store(S3StoreConfig{Region: "us-east-1", Bucket: "runtime", Endpoint: "http://s3.internal:9000"}, client); err == nil {
		t.Fatal("untrusted plaintext endpoint was accepted")
	}
	if _, err := newS3Store(S3StoreConfig{Region: "us-east-1", Bucket: "runtime", Endpoint: "http://s3.internal:9000", AllowInsecureEndpoint: true}, client); err != nil {
		t.Fatalf("explicit trusted plaintext endpoint was rejected: %v", err)
	}
	withoutSSE, err := newS3Store(S3StoreConfig{Region: "us-east-1", Bucket: "runtime", DisableServerSideEncryption: true}, client)
	if err != nil || withoutSSE.serverSideEncryption != "" {
		t.Fatalf("disabled server-side encryption=%q err=%v", withoutSSE.serverSideEncryption, err)
	}
	if descriptor := store.Descriptor(); descriptor.Provider != "domainry-s3" || descriptor.Revision != "v1" {
		t.Fatalf("descriptor=%+v", descriptor)
	}
}

func stageS3ForTest(t *testing.T, store *S3Store, stageID, content string) runtimefile.BlobInfo {
	t.Helper()
	staged, err := store.Stage(t.Context(), runtimefile.BlobStageRequest{
		WorkspaceID: "workspace-a", StageID: stageID, Content: strings.NewReader(content), MaxBytes: 64,
	})
	if err != nil {
		t.Fatal(err)
	}
	return staged
}
