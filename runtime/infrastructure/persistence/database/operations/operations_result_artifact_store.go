package operations

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	foundationartifact "github.com/domainry/domainry-foundation/artifact"
	"github.com/domainry/domainry-foundation/requestcontext"
	"github.com/domainry/domainry-runtime/pkg/runtimefile"
	operationspolicy "github.com/domainry/domainry-runtime/runtime/domain/operations/policy"
	operationsrepository "github.com/domainry/domainry-runtime/runtime/domain/operations/repository"
)

const (
	operationsResultArtifactOwner = "operations"
	operationsResultArtifactKind  = "result"
)

type OperationsResultArtifactStore struct {
	artifacts foundationartifact.Store
	blobs     runtimefile.BlobStore
}

func NewOperationsResultArtifactStore(artifacts foundationartifact.Store, blobs runtimefile.BlobStore) OperationsResultArtifactStore {
	return OperationsResultArtifactStore{artifacts: artifacts, blobs: blobs}
}

func (s OperationsResultArtifactStore) PutOperationsResult(ctx context.Context, value operationsrepository.OperationsResultArtifact) (operationsrepository.OperationsResultArtifactReference, error) {
	value.OperationID, value.WorkspaceID, value.CreatedBy = strings.TrimSpace(value.OperationID), strings.TrimSpace(value.WorkspaceID), strings.TrimSpace(value.CreatedBy)
	if s.artifacts == nil || s.blobs == nil || value.OperationID == "" || value.WorkspaceID == "" || value.CreatedBy == "" || value.CreatedAt.IsZero() || value.ExpiresAt.IsZero() || !value.ExpiresAt.After(value.CreatedAt) || !json.Valid(value.Content) {
		return operationsrepository.OperationsResultArtifactReference{}, fmt.Errorf("operations result artifact is invalid")
	}
	if len(value.Content) > operationspolicy.OperationsMaximumArtifactResultBytes {
		return operationsrepository.OperationsResultArtifactReference{}, fmt.Errorf("operations result artifact exceeds maximum size")
	}
	digest := sha256.Sum256(value.Content)
	digestText := hex.EncodeToString(digest[:])
	identityDigest := sha256.Sum256([]byte(value.WorkspaceID + "\x00" + value.OperationID))
	identity := hex.EncodeToString(identityDigest[:16])
	artifactID := "operation_result_" + identity
	blobKey := "operations-result-" + identity + "-" + digestText + ".json"
	stage, err := s.blobs.Stage(ctx, runtimefile.BlobStageRequest{
		WorkspaceID: value.WorkspaceID,
		StageID:     artifactID + ":" + requestcontext.NewRequestID(),
		Content:     bytes.NewReader(value.Content),
		MaxBytes:    int64(len(value.Content)) + 1,
	})
	if err != nil {
		return operationsrepository.OperationsResultArtifactReference{}, err
	}
	if stage.ContentSHA256 != digestText || stage.Size != int64(len(value.Content)) {
		return operationsrepository.OperationsResultArtifactReference{}, fmt.Errorf("operations result artifact stage integrity mismatch")
	}
	committed, err := s.blobs.Commit(ctx, runtimefile.BlobCommitRequest{
		WorkspaceID: value.WorkspaceID, StageKey: stage.BlobKey, BlobKey: blobKey,
		ContentSHA256: digestText, Size: int64(len(value.Content)),
	})
	if err != nil {
		return operationsrepository.OperationsResultArtifactReference{}, err
	}
	metadata := json.RawMessage(`{"schema":"operations.result-artifact.v1","redacted_in_operation":true}`)
	registration := foundationartifact.Artifact{
		ID: artifactID, WorkspaceID: value.WorkspaceID, Owner: operationsResultArtifactOwner, Kind: operationsResultArtifactKind,
		IdempotencyKey: value.OperationID, CreatedBy: value.CreatedBy, Filename: value.OperationID + "-result.json",
		MediaType: "application/json", ContentSHA256: committed.ContentSHA256, SizeBytes: committed.Size,
		StorageReference: committed.BlobKey, Status: foundationartifact.StatusAvailable, ScanStatus: foundationartifact.ScanNotRequired,
		ExpiresAt: value.ExpiresAt.UTC(), Metadata: metadata, CreatedAt: value.CreatedAt.UTC(), UpdatedAt: value.CreatedAt.UTC(),
	}
	artifact, _, err := s.artifacts.Register(ctx, registration)
	if err != nil {
		current, found, readErr := s.artifacts.ByID(ctx, value.WorkspaceID, artifactID)
		if readErr == nil && found && sameOperationsResultArtifact(current, registration) {
			artifact = current
		} else {
			cleanupErr := s.blobs.Delete(context.WithoutCancel(ctx), value.WorkspaceID, committed.BlobKey)
			return operationsrepository.OperationsResultArtifactReference{}, errors.Join(err, readErr, cleanupErr)
		}
	}
	bindingDigest := sha256.Sum256([]byte(value.WorkspaceID + "\x00" + artifact.ID + "\x00" + value.OperationID))
	_, _, err = s.artifacts.Bind(ctx, foundationartifact.Binding{
		ID: "operation_result_binding_" + hex.EncodeToString(bindingDigest[:16]), WorkspaceID: value.WorkspaceID,
		ArtifactID: artifact.ID, Owner: operationsResultArtifactOwner, Kind: foundationartifact.BindingOperation,
		ResourceType: "operation", ResourceID: value.OperationID, Metadata: json.RawMessage(`{"purpose":"result_replay"}`), CreatedAt: value.CreatedAt.UTC(),
	})
	if err != nil {
		return operationsrepository.OperationsResultArtifactReference{}, err
	}
	return operationsrepository.OperationsResultArtifactReference{ID: artifact.ID, ContentSHA256: artifact.ContentSHA256, SizeBytes: artifact.SizeBytes}, nil
}

func sameOperationsResultArtifact(left, right foundationartifact.Artifact) bool {
	return left.ID == right.ID && left.WorkspaceID == right.WorkspaceID && left.Owner == right.Owner && left.Kind == right.Kind &&
		left.IdempotencyKey == right.IdempotencyKey && left.CreatedBy == right.CreatedBy && left.Filename == right.Filename &&
		left.MediaType == right.MediaType && left.ContentSHA256 == right.ContentSHA256 && left.SizeBytes == right.SizeBytes &&
		left.StorageReference == right.StorageReference && left.Status == right.Status && left.ScanStatus == right.ScanStatus &&
		left.ExpiresAt.Equal(right.ExpiresAt) && string(left.Metadata) == string(right.Metadata)
}

func (s OperationsResultArtifactStore) GetOperationsResult(ctx context.Context, workspaceID, operationID, artifactID string) ([]byte, operationsrepository.OperationsResultArtifactReference, error) {
	workspaceID, operationID, artifactID = strings.TrimSpace(workspaceID), strings.TrimSpace(operationID), strings.TrimSpace(artifactID)
	if s.artifacts == nil || s.blobs == nil || workspaceID == "" || operationID == "" || artifactID == "" {
		return nil, operationsrepository.OperationsResultArtifactReference{}, fmt.Errorf("operations result artifact lookup is invalid")
	}
	artifact, found, err := s.artifacts.ByID(ctx, workspaceID, artifactID)
	if err != nil {
		return nil, operationsrepository.OperationsResultArtifactReference{}, err
	}
	if !found {
		return nil, operationsrepository.OperationsResultArtifactReference{}, fmt.Errorf("operations result artifact not found")
	}
	if artifact.Owner != operationsResultArtifactOwner || artifact.Kind != operationsResultArtifactKind || artifact.Status != foundationartifact.StatusAvailable || artifact.SizeBytes > operationspolicy.OperationsMaximumArtifactResultBytes {
		return nil, operationsrepository.OperationsResultArtifactReference{}, fmt.Errorf("operations result artifact is not available")
	}
	bindings, err := s.artifacts.Bindings(ctx, workspaceID, artifact.ID)
	if err != nil {
		return nil, operationsrepository.OperationsResultArtifactReference{}, err
	}
	bound := false
	for _, binding := range bindings {
		if binding.Owner == operationsResultArtifactOwner && binding.Kind == foundationartifact.BindingOperation && binding.ResourceType == "operation" && binding.ResourceID == operationID {
			bound = true
			break
		}
	}
	if !bound {
		return nil, operationsrepository.OperationsResultArtifactReference{}, fmt.Errorf("operations result artifact binding is missing")
	}
	reader, err := s.blobs.Open(ctx, workspaceID, artifact.StorageReference)
	if err != nil {
		return nil, operationsrepository.OperationsResultArtifactReference{}, err
	}
	defer reader.Close()
	content, err := io.ReadAll(io.LimitReader(reader, operationspolicy.OperationsMaximumArtifactResultBytes+1))
	if err != nil {
		return nil, operationsrepository.OperationsResultArtifactReference{}, err
	}
	if len(content) > operationspolicy.OperationsMaximumArtifactResultBytes || int64(len(content)) != artifact.SizeBytes {
		return nil, operationsrepository.OperationsResultArtifactReference{}, fmt.Errorf("operations result artifact size mismatch")
	}
	digest := sha256.Sum256(content)
	if hex.EncodeToString(digest[:]) != artifact.ContentSHA256 {
		return nil, operationsrepository.OperationsResultArtifactReference{}, fmt.Errorf("operations result artifact checksum mismatch")
	}
	return content, operationsrepository.OperationsResultArtifactReference{ID: artifact.ID, ContentSHA256: artifact.ContentSHA256, SizeBytes: artifact.SizeBytes}, nil
}

var _ operationsrepository.OperationsResultArtifactRepository = OperationsResultArtifactStore{}
