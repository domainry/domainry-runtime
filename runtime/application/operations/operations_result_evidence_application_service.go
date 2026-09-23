package operations

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	operationspolicy "github.com/domainry/domainry-runtime/runtime/domain/operations/policy"
	operationsrepository "github.com/domainry/domainry-runtime/runtime/domain/operations/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func (s *OperationsApplicationService) RegisterResultArtifacts(repository operationsrepository.OperationsResultArtifactRepository) error {
	if s == nil || repository == nil {
		return apperror.New(apperror.KindBadRequest, "backend.operations.result_artifact_registration_invalid", nil, nil)
	}
	s.resultArtifacts = repository
	return nil
}

func (s *OperationsApplicationService) prepareResultEvidence(ctx context.Context, receipt operationsmodel.OperationsReceipt) (operationsmodel.OperationsReceipt, error) {
	if len(bytes.TrimSpace(receipt.Result)) == 0 {
		return receipt, nil
	}
	redacted, err := operationspolicy.OperationsRedactResult(receipt.Result)
	if err != nil {
		return operationsmodel.OperationsReceipt{}, apperror.New(apperror.KindInternal, "backend.operations.receipt_result_invalid", err, nil)
	}
	if len(receipt.Result) <= operationspolicy.OperationsMaximumInlineResultBytes {
		receipt.Result = redacted
		return receipt, nil
	}
	if len(receipt.Result) > operationspolicy.OperationsMaximumArtifactResultBytes {
		return operationsmodel.OperationsReceipt{}, apperror.New(apperror.KindBadRequest, "backend.operations.result_too_large", nil, nil)
	}
	if s.resultArtifacts == nil {
		return operationsmodel.OperationsReceipt{}, apperror.New(apperror.KindInternal, "backend.operations.result_artifact_unavailable", nil, nil)
	}
	workspaceID := operationsResultWorkspaceID(receipt)
	if workspaceID == "" {
		return operationsmodel.OperationsReceipt{}, apperror.New(apperror.KindInternal, "backend.operations.result_artifact_workspace_unavailable", nil, nil)
	}
	reference, err := s.resultArtifacts.PutOperationsResult(ctx, operationsrepository.OperationsResultArtifact{
		OperationID: receipt.Command.ID,
		WorkspaceID: workspaceID,
		CreatedBy:   receipt.Command.RequestedBy,
		Content:     append([]byte(nil), receipt.Result...),
		CreatedAt:   receipt.Command.CreatedAt,
		ExpiresAt:   operationsResultExpiry(receipt.ExpiresAt),
	})
	if err != nil {
		return operationsmodel.OperationsReceipt{}, apperror.New(apperror.KindInternal, "backend.operations.result_artifact_write_failed", err, nil)
	}
	pointer := operationsmodel.OperationsResultEvidence{Artifact: &operationsmodel.OperationsResultArtifactPointer{
		Schema: operationsmodel.OperationsResultArtifactSchemaV1, ArtifactID: reference.ID,
		ContentSHA256: reference.ContentSHA256, SizeBytes: reference.SizeBytes,
	}}
	receipt.Result, _ = json.Marshal(pointer)
	receipt.Evidence = appendUniqueOperationsEvidence(receipt.Evidence, "artifact:"+reference.ID)
	return receipt, nil
}

func operationsResultExpiry(value string) time.Time {
	parsed, _ := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	return parsed
}

func (s *OperationsApplicationService) receiptResult(ctx context.Context, receipt operationsmodel.OperationsReceipt) (json.RawMessage, error) {
	if len(bytes.TrimSpace(receipt.Result)) == 0 {
		return nil, nil
	}
	var evidence operationsmodel.OperationsResultEvidence
	if json.Unmarshal(receipt.Result, &evidence) != nil || evidence.Artifact == nil || evidence.Artifact.Schema != operationsmodel.OperationsResultArtifactSchemaV1 {
		return append(json.RawMessage(nil), receipt.Result...), nil
	}
	if s.resultArtifacts == nil {
		return nil, apperror.New(apperror.KindInternal, "backend.operations.result_artifact_unavailable", nil, nil)
	}
	content, reference, err := s.resultArtifacts.GetOperationsResult(ctx, operationsResultWorkspaceID(receipt), receipt.Command.ID, evidence.Artifact.ArtifactID)
	if err != nil {
		return nil, apperror.New(apperror.KindInternal, "backend.operations.result_artifact_read_failed", err, nil)
	}
	digest := sha256.Sum256(content)
	if reference.ID != evidence.Artifact.ArtifactID || reference.SizeBytes != evidence.Artifact.SizeBytes || int64(len(content)) != evidence.Artifact.SizeBytes ||
		reference.ContentSHA256 != evidence.Artifact.ContentSHA256 || hex.EncodeToString(digest[:]) != evidence.Artifact.ContentSHA256 {
		return nil, apperror.New(apperror.KindInternal, "backend.operations.result_artifact_integrity_failed", nil, nil)
	}
	if !json.Valid(content) {
		return nil, apperror.New(apperror.KindInternal, "backend.operations.result_artifact_invalid", nil, nil)
	}
	return json.RawMessage(content), nil
}

func operationsResultWorkspaceID(receipt operationsmodel.OperationsReceipt) string {
	if workspaceID := strings.TrimSpace(receipt.Command.Scope.WorkspaceID); workspaceID != "" {
		return workspaceID
	}
	return strings.TrimSpace(principalmodel.InstallationWorkspaceID)
}

func appendUniqueOperationsEvidence(values []string, value string) []string {
	value = strings.TrimSpace(value)
	for _, existing := range values {
		if strings.TrimSpace(existing) == value {
			return values
		}
	}
	return append(values, value)
}
