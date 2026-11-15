package blobstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/domainry/domainry-foundation/telemetry"
	"github.com/domainry/domainry-runtime/pkg/runtimefile"
)

const defaultS3BlobPrefix = "domainry-runtime"

type S3StoreConfig struct {
	Region         string
	Bucket         string
	Prefix         string
	Endpoint       string
	ForcePathStyle bool
	// AllowInsecureEndpoint must be explicit for trusted local or in-cluster
	// S3-compatible endpoints that do not terminate TLS.
	AllowInsecureEndpoint bool
	// DisableServerSideEncryption is only for S3-compatible deployments whose
	// storage layer provides encryption and does not implement the S3 SSE-AES256
	// contract correctly. AWS S3 keeps SSE-AES256 enabled by default.
	DisableServerSideEncryption bool
}

type s3BlobClient interface {
	PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	HeadObject(context.Context, *s3.HeadObjectInput, ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
	DeleteObject(context.Context, *s3.DeleteObjectInput, ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
}

type S3Store struct {
	client               s3BlobClient
	bucket               string
	prefix               string
	serverSideEncryption types.ServerSideEncryption
}

func NewS3Store(ctx context.Context, config S3StoreConfig) (*S3Store, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}
	httpClient := telemetry.InstrumentHTTPClientWithOptions(http.DefaultClient, "domainry.runtime.s3", telemetry.HTTPClientInstrumentationOptions{DisableDiagnosticContent: true})
	configuration, err := awsconfig.LoadDefaultConfig(
		telemetry.WithHTTPClientOperation(ctx, "runtime.s3.configure"),
		awsconfig.WithRegion(strings.TrimSpace(config.Region)),
		awsconfig.WithHTTPClient(httpClient),
		// Compatible S3 endpoints commonly run without TLS inside a trusted
		// cluster. Required-only checksum calculation avoids an SDK trailer for
		// the non-seekable stream copied from the staging object.
		awsconfig.WithRequestChecksumCalculation(aws.RequestChecksumCalculationWhenRequired),
		awsconfig.WithResponseChecksumValidation(aws.ResponseChecksumValidationWhenRequired),
	)
	if err != nil {
		return nil, fmt.Errorf("load Runtime S3 configuration: %w", err)
	}
	if _, err = configuration.Credentials.Retrieve(telemetry.WithHTTPClientOperation(ctx, "runtime.s3.credentials")); err != nil {
		return nil, fmt.Errorf("retrieve Runtime S3 credentials: %w", err)
	}
	client := s3.NewFromConfig(configuration, func(options *s3.Options) {
		if endpoint := strings.TrimSpace(config.Endpoint); endpoint != "" {
			options.BaseEndpoint = aws.String(endpoint)
		}
		options.UsePathStyle = config.ForcePathStyle
	})
	return newS3Store(config, client)
}

func newS3Store(config S3StoreConfig, client s3BlobClient) (*S3Store, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}
	if client == nil {
		return nil, errors.New("Runtime S3 client is required")
	}
	prefix := strings.Trim(strings.TrimSpace(config.Prefix), "/")
	if prefix == "" {
		prefix = defaultS3BlobPrefix
	}
	if path.Clean(prefix) != prefix || strings.HasPrefix(prefix, "..") {
		return nil, errors.New("Runtime S3 prefix must be a clean relative path")
	}
	serverSideEncryption := types.ServerSideEncryptionAes256
	if config.DisableServerSideEncryption {
		serverSideEncryption = ""
	}
	return &S3Store{
		client: client, bucket: strings.TrimSpace(config.Bucket), prefix: prefix,
		serverSideEncryption: serverSideEncryption,
	}, nil
}

func (config S3StoreConfig) validate() error {
	if strings.TrimSpace(config.Region) == "" {
		return errors.New("Runtime S3 region is required")
	}
	if strings.TrimSpace(config.Bucket) == "" {
		return errors.New("Runtime S3 bucket is required")
	}
	endpoint := strings.TrimSpace(config.Endpoint)
	if endpoint == "" {
		return nil
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" && parsed.Path != "/" {
		return errors.New("Runtime S3 endpoint is invalid")
	}
	if parsed.Scheme != "https" && !config.AllowInsecureEndpoint && !runtimeS3LoopbackHTTP(parsed) {
		return errors.New("Runtime S3 endpoint must be HTTPS unless explicitly trusted")
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return errors.New("Runtime S3 endpoint must use HTTP or HTTPS")
	}
	return nil
}

func (*S3Store) Descriptor() runtimefile.AdapterDescriptor {
	return runtimefile.AdapterDescriptor{Provider: "domainry-s3", Revision: "v1"}
}

func (store *S3Store) Stage(ctx context.Context, request runtimefile.BlobStageRequest) (result runtimefile.BlobInfo, returnErr error) {
	started := time.Now()
	defer func() {
		recordS3BlobCall(ctx, "stage", map[string]any{"stage_hash": localDiagnosticHash(request.StageID), "max_bytes": request.MaxBytes}, result, started, returnErr)
	}()
	if err := ctx.Err(); err != nil {
		return runtimefile.BlobInfo{}, err
	}
	request.WorkspaceID, request.StageID = strings.TrimSpace(request.WorkspaceID), strings.TrimSpace(request.StageID)
	if request.WorkspaceID == "" || request.StageID == "" || request.Content == nil || request.MaxBytes < 1 {
		return runtimefile.BlobInfo{}, errors.New("Runtime S3 blob stage request is invalid")
	}
	file, digest, size, err := spoolS3Blob(ctx, request.Content, request.MaxBytes)
	if err != nil {
		return runtimefile.BlobInfo{}, err
	}
	defer func() {
		_ = file.Close()
		_ = os.Remove(file.Name())
	}()
	stageDigest := sha256.Sum256([]byte(request.StageID))
	stageKey := "stage-" + hex.EncodeToString(stageDigest[:16])
	objectKey := store.stageObjectKey(request.WorkspaceID, stageKey)
	metadata := s3BlobMetadata(digest, size)
	_, err = store.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(store.bucket), Key: aws.String(objectKey), Body: file,
		ContentLength: aws.Int64(size), ContentType: aws.String("application/octet-stream"),
		IfNoneMatch: aws.String("*"), ServerSideEncryption: store.serverSideEncryption,
		Metadata: metadata,
	})
	if err != nil {
		if s3PreconditionFailed(err) {
			return runtimefile.BlobInfo{}, runtimefile.ErrBlobIdentityConflict
		}
		existing, statErr := store.statObject(ctx, request.WorkspaceID, stageKey, objectKey)
		if statErr == nil && existing.ContentSHA256 == digest && existing.Size == size {
			return existing, nil
		}
		return runtimefile.BlobInfo{}, fmt.Errorf("stage Runtime S3 blob: %w", err)
	}
	return runtimefile.BlobInfo{WorkspaceID: request.WorkspaceID, BlobKey: stageKey, ContentSHA256: digest, Size: size}, nil
}

func (store *S3Store) Commit(ctx context.Context, request runtimefile.BlobCommitRequest) (result runtimefile.BlobInfo, returnErr error) {
	started := time.Now()
	defer func() {
		recordS3BlobCall(ctx, "commit", map[string]any{
			"stage_hash": localDiagnosticHash(request.StageKey), "blob_hash": localDiagnosticHash(request.BlobKey), "size": request.Size,
		}, result, started, returnErr)
	}()
	if err := ctx.Err(); err != nil {
		return runtimefile.BlobInfo{}, err
	}
	request.WorkspaceID = strings.TrimSpace(request.WorkspaceID)
	request.StageKey = strings.TrimSpace(request.StageKey)
	request.BlobKey = strings.TrimSpace(request.BlobKey)
	request.ContentSHA256 = strings.ToLower(strings.TrimSpace(request.ContentSHA256))
	if request.WorkspaceID == "" || !localStageKeyPattern.MatchString(request.StageKey) || !validLocalBlobKey(request.BlobKey) || localStageKeyPattern.MatchString(request.BlobKey) || !localContentSHA256Pattern.MatchString(request.ContentSHA256) || request.Size < 0 || !strings.Contains(request.BlobKey, request.ContentSHA256) {
		return runtimefile.BlobInfo{}, errors.New("Runtime S3 blob commit request is invalid")
	}
	stageObjectKey := store.stageObjectKey(request.WorkspaceID, request.StageKey)
	destinationKey := store.blobObjectKey(request.WorkspaceID, request.BlobKey)
	staged, err := store.statObject(ctx, request.WorkspaceID, request.StageKey, stageObjectKey)
	if err != nil {
		if errors.Is(err, runtimefile.ErrBlobNotFound) {
			return store.resolveCommittedBlob(ctx, request)
		}
		return runtimefile.BlobInfo{}, err
	}
	if staged.Size != request.Size || !strings.EqualFold(staged.ContentSHA256, request.ContentSHA256) {
		return runtimefile.BlobInfo{}, runtimefile.ErrBlobIdentityConflict
	}
	opened, err := store.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(store.bucket), Key: aws.String(stageObjectKey)})
	if s3Missing(err) {
		return store.resolveCommittedBlob(ctx, request)
	}
	if err != nil {
		return runtimefile.BlobInfo{}, fmt.Errorf("open staged Runtime S3 blob: %w", err)
	}
	stagedFile, actualDigest, actualSize, spoolErr := spoolS3Blob(ctx, opened.Body, request.Size)
	closeErr := opened.Body.Close()
	if spoolErr != nil {
		if errors.Is(spoolErr, runtimefile.ErrBlobTooLarge) {
			return runtimefile.BlobInfo{}, runtimefile.ErrBlobIdentityConflict
		}
		return runtimefile.BlobInfo{}, spoolErr
	}
	defer func() {
		_ = stagedFile.Close()
		_ = os.Remove(stagedFile.Name())
	}()
	if closeErr != nil {
		return runtimefile.BlobInfo{}, closeErr
	}
	if actualSize != request.Size || !strings.EqualFold(actualDigest, request.ContentSHA256) {
		return runtimefile.BlobInfo{}, fmt.Errorf("%w: staged content expected size=%d sha256=%s, got size=%d sha256=%s", runtimefile.ErrBlobIdentityConflict, request.Size, request.ContentSHA256, actualSize, actualDigest)
	}
	putInput := &s3.PutObjectInput{
		Bucket: aws.String(store.bucket), Key: aws.String(destinationKey), Body: stagedFile,
		ContentLength: aws.Int64(request.Size), ContentType: aws.String("application/octet-stream"),
		IfNoneMatch: aws.String("*"), ServerSideEncryption: store.serverSideEncryption,
		Metadata: s3BlobMetadata(request.ContentSHA256, request.Size),
	}
	_, putErr := store.client.PutObject(ctx, putInput)
	if putErr != nil {
		existing, resolveErr := store.resolveCommittedBlob(ctx, request)
		if resolveErr == nil {
			if deleteErr := store.deleteObject(ctx, stageObjectKey); deleteErr != nil {
				return runtimefile.BlobInfo{}, deleteErr
			}
			return existing, nil
		}
		if s3PreconditionFailed(putErr) || errors.Is(resolveErr, runtimefile.ErrBlobIdentityConflict) {
			return runtimefile.BlobInfo{}, fmt.Errorf("%w: conditional destination write failed: %v", runtimefile.ErrBlobIdentityConflict, putErr)
		}
		return runtimefile.BlobInfo{}, fmt.Errorf("commit Runtime S3 blob: %w", putErr)
	}
	committed, err := store.statObject(ctx, request.WorkspaceID, request.BlobKey, destinationKey)
	if err != nil {
		return runtimefile.BlobInfo{}, err
	}
	if committed.Size != request.Size || !strings.EqualFold(committed.ContentSHA256, request.ContentSHA256) {
		return runtimefile.BlobInfo{}, runtimefile.ErrBlobIdentityConflict
	}
	if err = store.deleteObject(ctx, stageObjectKey); err != nil {
		return runtimefile.BlobInfo{}, err
	}
	return committed, nil
}

func (store *S3Store) resolveCommittedBlob(ctx context.Context, request runtimefile.BlobCommitRequest) (runtimefile.BlobInfo, error) {
	existing, err := store.statObject(ctx, request.WorkspaceID, request.BlobKey, store.blobObjectKey(request.WorkspaceID, request.BlobKey))
	if errors.Is(err, runtimefile.ErrBlobNotFound) {
		return runtimefile.BlobInfo{}, runtimefile.ErrBlobIdentityConflict
	}
	if err != nil {
		return runtimefile.BlobInfo{}, err
	}
	if existing.Size != request.Size || !strings.EqualFold(existing.ContentSHA256, request.ContentSHA256) {
		return runtimefile.BlobInfo{}, runtimefile.ErrBlobIdentityConflict
	}
	return existing, nil
}

func (store *S3Store) Open(ctx context.Context, workspaceID, blobKey string) (reader io.ReadCloser, returnErr error) {
	started := time.Now()
	defer func() {
		telemetry.RecordDependencyCall(ctx, telemetry.DependencyCall{
			Feature: telemetry.DiagnosticFeatureObjectStore, Dependency: "s3_blobstore", Operation: "open",
			Input: map[string]any{"blob_hash": localDiagnosticHash(blobKey)},
		}, telemetry.DependencyResult{Output: map[string]any{"opened": reader != nil}, Err: returnErr}, time.Since(started))
	}()
	objectKey, err := store.objectKey(workspaceID, blobKey)
	if err != nil {
		return nil, err
	}
	output, err := store.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(store.bucket), Key: aws.String(objectKey)})
	if s3Missing(err) {
		return nil, runtimefile.ErrBlobNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("open Runtime S3 blob: %w", err)
	}
	if _, validErr := s3BlobInfo(strings.TrimSpace(workspaceID), strings.TrimSpace(blobKey), output.ContentLength, output.Metadata, time.Time{}); validErr != nil {
		_ = output.Body.Close()
		return nil, validErr
	}
	return output.Body, nil
}

func (store *S3Store) Stat(ctx context.Context, workspaceID, blobKey string) (result runtimefile.BlobInfo, returnErr error) {
	started := time.Now()
	defer func() {
		recordS3BlobCall(ctx, "stat", map[string]any{"blob_hash": localDiagnosticHash(blobKey)}, result, started, returnErr)
	}()
	objectKey, err := store.objectKey(workspaceID, blobKey)
	if err != nil {
		return runtimefile.BlobInfo{}, err
	}
	return store.statObject(ctx, strings.TrimSpace(workspaceID), strings.TrimSpace(blobKey), objectKey)
}

func (store *S3Store) statObject(ctx context.Context, workspaceID, blobKey, objectKey string) (runtimefile.BlobInfo, error) {
	output, err := store.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(store.bucket), Key: aws.String(objectKey)})
	if s3Missing(err) {
		return runtimefile.BlobInfo{}, runtimefile.ErrBlobNotFound
	}
	if err != nil {
		return runtimefile.BlobInfo{}, fmt.Errorf("stat Runtime S3 blob: %w", err)
	}
	modified := time.Time{}
	if output.LastModified != nil {
		modified = output.LastModified.UTC()
	}
	return s3BlobInfo(workspaceID, blobKey, output.ContentLength, output.Metadata, modified)
}

func (store *S3Store) Delete(ctx context.Context, workspaceID, blobKey string) (returnErr error) {
	started := time.Now()
	defer func() {
		telemetry.RecordDependencyCall(ctx, telemetry.DependencyCall{
			Feature: telemetry.DiagnosticFeatureObjectStore, Dependency: "s3_blobstore", Operation: "delete",
			Input: map[string]any{"blob_hash": localDiagnosticHash(blobKey)},
		}, telemetry.DependencyResult{Err: returnErr}, time.Since(started))
	}()
	objectKey, err := store.objectKey(workspaceID, blobKey)
	if err != nil {
		return err
	}
	return store.deleteObject(ctx, objectKey)
}

func (store *S3Store) deleteObject(ctx context.Context, objectKey string) error {
	_, err := store.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(store.bucket), Key: aws.String(objectKey)})
	if s3Missing(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("delete Runtime S3 blob: %w", err)
	}
	return nil
}

func (store *S3Store) objectKey(workspaceID, blobKey string) (string, error) {
	workspaceID, blobKey = strings.TrimSpace(workspaceID), strings.TrimSpace(blobKey)
	if workspaceID == "" || !validLocalBlobKey(blobKey) {
		return "", errors.New("Runtime S3 blob identity is invalid")
	}
	if localStageKeyPattern.MatchString(blobKey) {
		return store.stageObjectKey(workspaceID, blobKey), nil
	}
	return store.blobObjectKey(workspaceID, blobKey), nil
}

func (store *S3Store) stageObjectKey(workspaceID, stageKey string) string {
	return store.workspacePrefix(workspaceID) + "/staging/" + stageKey
}

func (store *S3Store) blobObjectKey(workspaceID, blobKey string) string {
	return store.workspacePrefix(workspaceID) + "/blobs/" + blobKey
}

func (store *S3Store) workspacePrefix(workspaceID string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(workspaceID)))
	return store.prefix + "/workspace-" + hex.EncodeToString(digest[:16])
}

func spoolS3Blob(ctx context.Context, content io.Reader, maxBytes int64) (*os.File, string, int64, error) {
	file, err := os.CreateTemp("", "domainry-runtime-s3-stage-*")
	if err != nil {
		return nil, "", 0, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = file.Close()
			_ = os.Remove(file.Name())
		}
	}()
	hash := sha256.New()
	limited := &io.LimitedReader{R: contextReader{ctx: ctx, reader: content}, N: maxBytes + 1}
	size, err := io.Copy(io.MultiWriter(file, hash), limited)
	if err != nil {
		return nil, "", 0, err
	}
	if size > maxBytes {
		return nil, "", 0, runtimefile.ErrBlobTooLarge
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return nil, "", 0, err
	}
	keep = true
	return file, hex.EncodeToString(hash.Sum(nil)), size, nil
}

func s3BlobMetadata(contentSHA256 string, size int64) map[string]string {
	return map[string]string{"content-sha256": contentSHA256, "content-size": strconv.FormatInt(size, 10)}
}

func s3BlobInfo(workspaceID, blobKey string, contentLength *int64, metadata map[string]string, modified time.Time) (runtimefile.BlobInfo, error) {
	contentSHA256 := strings.ToLower(strings.TrimSpace(metadata["content-sha256"]))
	declaredSize, err := strconv.ParseInt(strings.TrimSpace(metadata["content-size"]), 10, 64)
	size := aws.ToInt64(contentLength)
	if err != nil || declaredSize < 0 || size != declaredSize || !localContentSHA256Pattern.MatchString(contentSHA256) {
		return runtimefile.BlobInfo{}, runtimefile.ErrBlobIdentityConflict
	}
	return runtimefile.BlobInfo{WorkspaceID: workspaceID, BlobKey: blobKey, ContentSHA256: contentSHA256, Size: size, ModifiedAt: modified}, nil
}

func s3Missing(err error) bool {
	if err == nil {
		return false
	}
	var noSuchKey *types.NoSuchKey
	if errors.As(err, &noSuchKey) {
		return true
	}
	var apiError smithy.APIError
	if errors.As(err, &apiError) {
		switch apiError.ErrorCode() {
		case "NoSuchKey", "NotFound", "NoSuchBucket":
			return true
		}
	}
	return false
}

func s3PreconditionFailed(err error) bool {
	var apiError smithy.APIError
	return errors.As(err, &apiError) && (apiError.ErrorCode() == "PreconditionFailed" || apiError.ErrorCode() == "ConditionalRequestConflict")
}

func runtimeS3LoopbackHTTP(value *url.URL) bool {
	if value.Scheme != "http" {
		return false
	}
	switch value.Hostname() {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}

func recordS3BlobCall(ctx context.Context, operation string, input map[string]any, result runtimefile.BlobInfo, started time.Time, err error) {
	telemetry.RecordDependencyCall(ctx, telemetry.DependencyCall{
		Feature: telemetry.DiagnosticFeatureObjectStore, Dependency: "s3_blobstore", Operation: operation, Input: input,
	}, telemetry.DependencyResult{Output: map[string]any{
		"blob_hash": localDiagnosticHash(result.BlobKey), "size": result.Size, "content_sha256": result.ContentSHA256,
	}, Err: err}, time.Since(started))
}

var _ runtimefile.BlobStore = (*S3Store)(nil)
