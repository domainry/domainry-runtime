package operations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	operationscontract "github.com/domainry/domainry-runtime/runtime/domain/operations/contract"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	operationsrepository "github.com/domainry/domainry-runtime/runtime/domain/operations/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

type operationsResultArtifactProbe struct {
	values map[string]operationsrepository.OperationsResultArtifact
}

func (p *operationsResultArtifactProbe) PutOperationsResult(_ context.Context, value operationsrepository.OperationsResultArtifact) (operationsrepository.OperationsResultArtifactReference, error) {
	if p.values == nil {
		p.values = map[string]operationsrepository.OperationsResultArtifact{}
	}
	digest := sha256.Sum256(value.Content)
	reference := operationsrepository.OperationsResultArtifactReference{ID: "artifact-" + value.OperationID, ContentSHA256: hex.EncodeToString(digest[:]), SizeBytes: int64(len(value.Content))}
	p.values[reference.ID] = value
	return reference, nil
}

func (p *operationsResultArtifactProbe) GetOperationsResult(_ context.Context, workspaceID, operationID, artifactID string) ([]byte, operationsrepository.OperationsResultArtifactReference, error) {
	value := p.values[artifactID]
	digest := sha256.Sum256(value.Content)
	return append([]byte(nil), value.Content...), operationsrepository.OperationsResultArtifactReference{ID: artifactID, ContentSHA256: hex.EncodeToString(digest[:]), SizeBytes: int64(len(value.Content))}, nil
}

func TestOperationsLargeResultUsesArtifactAndReplaysIntegrityCheckedContent(t *testing.T) {
	repository := &operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}
	service := NewOperationsApplicationService(repository, nil, nil, func() string { return "large-result" })
	artifacts := &operationsResultArtifactProbe{}
	if err := service.RegisterResultArtifacts(artifacts); err != nil {
		t.Fatal(err)
	}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "operator"}}, accessfixture.Bundle{Permissions: []string{operationscontract.ActionEnableAutomationRule}})
	request := OperationsOwnerExecutionRequest{
		Kind: "automation.rule.enable", ResourceType: "automation_rule", ResourceID: "rule-1", Reason: "enable", Key: "large",
		ReplayReadiness: func(context.Context, any) error { return nil },
	}
	large := strings.Repeat("evidence-", 3000)
	executions := 0
	first, err := service.ExecuteOwnerOperation(t.Context(), request, principal, func(context.Context) (any, error) {
		executions++
		return map[string]any{"rule_id": "rule-1", "details": large, "access_token": "owner-secret"}, nil
	})
	if err != nil || first.Replayed || executions != 1 {
		t.Fatalf("first=%#v executions=%d err=%v", first, executions, err)
	}
	var stored operationsmodel.OperationsReceipt
	for _, receipt := range repository.receipts {
		stored = receipt
	}
	if len(stored.Result) >= len(large) || strings.Contains(string(stored.Result), "owner-secret") || len(artifacts.values) != 1 {
		t.Fatalf("stored result=%s artifacts=%d", stored.Result, len(artifacts.values))
	}
	for _, artifact := range artifacts.values {
		if artifact.ExpiresAt.IsZero() || !artifact.ExpiresAt.After(artifact.CreatedAt) {
			t.Fatalf("artifact retention=%#v", artifact)
		}
	}
	var pointer operationsmodel.OperationsResultEvidence
	if err := json.Unmarshal(stored.Result, &pointer); err != nil || pointer.Artifact == nil {
		t.Fatalf("artifact pointer=%#v err=%v", pointer, err)
	}
	replayed, err := service.ExecuteOwnerOperation(t.Context(), request, principal, func(context.Context) (any, error) {
		executions++
		return nil, nil
	})
	if err != nil || !replayed.Replayed || executions != 1 {
		t.Fatalf("replay=%#v executions=%d err=%v", replayed, executions, err)
	}
	value, ok := replayed.Value.(map[string]any)
	if !ok || value["details"] != large || value["access_token"] != "owner-secret" {
		t.Fatalf("artifact replay value=%#v", replayed.Value)
	}
}

func TestOperationsSmallResultIsRedactedBeforeReceiptPersistence(t *testing.T) {
	repository := &operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}
	service := NewOperationsApplicationService(repository, nil, nil, func() string { return "redacted-result" })
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "operator"}}, accessfixture.Bundle{Permissions: []string{operationscontract.ActionEnableAutomationRule}})
	request := OperationsOwnerExecutionRequest{
		Kind: "automation.rule.enable", ResourceType: "automation_rule", ResourceID: "rule-1", Reason: "enable", Key: "redacted",
		ReplayReadiness: func(context.Context, any) error { return nil },
	}
	if _, err := service.ExecuteOwnerOperation(t.Context(), request, principal, func(context.Context) (any, error) {
		return map[string]any{"rule_id": "rule-1", "password": "owner-secret"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, receipt := range repository.receipts {
		if strings.Contains(string(receipt.Result), "owner-secret") || !strings.Contains(string(receipt.Result), "[REDACTED]") {
			t.Fatalf("unredacted receipt=%s", receipt.Result)
		}
	}
}
