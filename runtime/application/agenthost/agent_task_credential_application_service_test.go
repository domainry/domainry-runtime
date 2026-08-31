package agenthost

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-foundation/apperror"
)

type agentCredentialIDStub struct{}

func (agentCredentialIDStub) NewID() string { return "nonce" }

func TestAgentTaskCredentialIsShortLivedAndStrictlyScoped(t *testing.T) {
	now := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	clock := &agentTaskClock{now: now}
	service := NewAgentTaskCredentialApplicationService([]byte(strings.Repeat("k", 32)), clock, agentCredentialIDStub{})
	claims := AgentTaskCredentialClaims{
		WorkspaceID: "workspace-a", ProcessID: "process-a", TaskRunID: "run-a", AllowedTools: []string{"query_records", "query_records", "invoke_action"},
		Principal: agentsdk.PrincipalReference{UserID: "user-a", RoleKey: "operator", WorkspaceID: "workspace-a", AuthorizationRevision: "auth-1"},
	}
	token, err := service.Issue(t.Context(), claims, 5*time.Minute)
	if err != nil || strings.Count(token, ".") != 1 {
		t.Fatalf("token=%q err=%v", token, err)
	}
	verified, err := service.Verify(t.Context(), token, AgentTaskCredentialScope{WorkspaceID: "workspace-a", ProcessID: "process-a", TaskRunID: "run-a", Tool: "query_records"})
	if err != nil || verified.Nonce != "agent_credential_nonce" || len(verified.AllowedTools) != 2 || verified.Principal.AuthorizationRevision != "auth-1" {
		t.Fatalf("verified=%#v err=%v", verified, err)
	}
	for name, scope := range map[string]AgentTaskCredentialScope{
		"workspace": {WorkspaceID: "workspace-b", ProcessID: "process-a", TaskRunID: "run-a", Tool: "query_records"},
		"process":   {WorkspaceID: "workspace-a", ProcessID: "process-b", TaskRunID: "run-a", Tool: "query_records"},
		"task":      {WorkspaceID: "workspace-a", ProcessID: "process-a", TaskRunID: "run-b", Tool: "query_records"},
		"tool":      {WorkspaceID: "workspace-a", ProcessID: "process-a", TaskRunID: "run-a", Tool: "delete_database"},
	} {
		if _, err := service.Verify(t.Context(), token, scope); err == nil {
			t.Fatalf("%s scope accepted", name)
		}
	}
	parts := strings.Split(token, ".")
	tampered := parts[0][:len(parts[0])-1] + "A." + parts[1]
	if _, err := service.Verify(t.Context(), tampered, AgentTaskCredentialScope{}); apperror.CodeOf(err) != "agent.credential.signature_invalid" {
		t.Fatalf("tamper err=%v", err)
	}
	clock.now = now.Add(5 * time.Minute)
	if _, err := service.Verify(t.Context(), token, AgentTaskCredentialScope{WorkspaceID: "workspace-a", ProcessID: "process-a", TaskRunID: "run-a"}); apperror.CodeOf(err) != "agent.credential.expired" {
		t.Fatalf("expiry err=%v", err)
	}
}

func TestAgentTaskCredentialRejectsUnsafeConfigurationAndClaims(t *testing.T) {
	now := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	claims := AgentTaskCredentialClaims{WorkspaceID: "workspace", TaskRunID: "run", Principal: agentsdk.PrincipalReference{UserID: "user", RoleKey: "role"}}
	if _, err := NewAgentTaskCredentialApplicationService([]byte("short"), agentTaskClock{now: now}, nil).Issue(t.Context(), claims, time.Minute); apperror.CodeOf(err) != "agent.credential.signer_unavailable" {
		t.Fatalf("short key err=%v", err)
	}
	service := NewAgentTaskCredentialApplicationService([]byte(strings.Repeat("k", 32)), agentTaskClock{now: now}, nil)
	if _, err := service.Issue(t.Context(), AgentTaskCredentialClaims{}, time.Minute); apperror.CodeOf(err) != "agent.credential.claims_invalid" {
		t.Fatalf("claims err=%v", err)
	}
	if _, err := service.Issue(t.Context(), claims, 16*time.Minute); apperror.CodeOf(err) != "agent.credential.claims_invalid" {
		t.Fatalf("ttl err=%v", err)
	}
	if _, err := service.Verify(t.Context(), "bad", AgentTaskCredentialScope{}); apperror.CodeOf(err) != "agent.credential.malformed" {
		t.Fatalf("malformed err=%v", err)
	}
}

func TestAgentTaskCredentialBoundaryMatrix(t *testing.T) {
	now := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	key := []byte(strings.Repeat("k", 32))
	service := NewAgentTaskCredentialApplicationService(key, agentTaskClock{now: now}, agentCredentialIDStub{})
	if defaults := NewAgentTaskCredentialApplicationService(key, nil, nil); defaults.clock == nil || defaults.ids == nil {
		t.Fatal("default credential dependencies missing")
	}
	valid := AgentTaskCredentialClaims{WorkspaceID: "workspace", ProcessID: "process", TaskRunID: "run", Principal: agentsdk.PrincipalReference{UserID: "user", RoleKey: "role"}, AllowedTools: []string{"query"}, Nonce: "provided"}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.Issue(cancelled, valid, time.Minute); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled issue=%v", err)
	}
	if _, err := service.Verify(cancelled, "", AgentTaskCredentialScope{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled verify=%v", err)
	}
	for name, candidate := range map[string]*AgentTaskCredentialApplicationService{
		"nil":   nil,
		"short": NewAgentTaskCredentialApplicationService([]byte("short"), agentTaskClock{now: now}, nil),
	} {
		if _, err := candidate.Issue(t.Context(), valid, time.Minute); apperror.CodeOf(err) != "agent.credential.signer_unavailable" {
			t.Fatalf("%s issue=%v", name, err)
		}
		if _, err := candidate.Verify(t.Context(), "bad", AgentTaskCredentialScope{}); apperror.CodeOf(err) != "agent.credential.signer_unavailable" {
			t.Fatalf("%s verify=%v", name, err)
		}
	}
	for name, mutate := range map[string]func(*AgentTaskCredentialClaims, *time.Duration){
		"workspace": func(value *AgentTaskCredentialClaims, _ *time.Duration) { value.WorkspaceID = "" },
		"task":      func(value *AgentTaskCredentialClaims, _ *time.Duration) { value.TaskRunID = "" },
		"user":      func(value *AgentTaskCredentialClaims, _ *time.Duration) { value.Principal.UserID = "" },
		"role":      func(value *AgentTaskCredentialClaims, _ *time.Duration) { value.Principal.RoleKey = "" },
		"zero ttl":  func(_ *AgentTaskCredentialClaims, ttl *time.Duration) { *ttl = 0 },
		"long ttl":  func(_ *AgentTaskCredentialClaims, ttl *time.Duration) { *ttl = 16 * time.Minute },
	} {
		t.Run(name, func(t *testing.T) {
			candidate, ttl := valid, time.Minute
			mutate(&candidate, &ttl)
			if _, err := service.Issue(t.Context(), candidate, ttl); apperror.CodeOf(err) != "agent.credential.claims_invalid" {
				t.Fatalf("error=%v", err)
			}
		})
	}
	token, err := service.Issue(t.Context(), valid, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	validScope := AgentTaskCredentialScope{WorkspaceID: valid.WorkspaceID, ProcessID: valid.ProcessID, TaskRunID: valid.TaskRunID}
	if verified, err := service.Verify(t.Context(), token, validScope); err != nil || verified.Nonce != "provided" {
		t.Fatalf("provided nonce=%#v err=%v", verified, err)
	}
	if _, err := service.Verify(t.Context(), "payload.%", validScope); apperror.CodeOf(err) != "agent.credential.signature_invalid" {
		t.Fatalf("bad signature encoding=%v", err)
	}
	if _, err := service.Verify(t.Context(), signedCredentialPayload(service, "%"), validScope); apperror.CodeOf(err) != "agent.credential.malformed" {
		t.Fatalf("bad payload encoding=%v", err)
	}
	if _, err := service.Verify(t.Context(), signedCredentialPayload(service, base64.RawURLEncoding.EncodeToString([]byte("{"))), validScope); apperror.CodeOf(err) != "agent.credential.version_invalid" {
		t.Fatalf("bad json=%v", err)
	}
	wrongVersion := valid
	wrongVersion.Version, wrongVersion.IssuedAt, wrongVersion.ExpiresAt = "wrong", now, now.Add(time.Minute)
	if _, err := service.Verify(t.Context(), signedCredentialClaims(t, service, wrongVersion), validScope); apperror.CodeOf(err) != "agent.credential.version_invalid" {
		t.Fatalf("wrong version=%v", err)
	}
	future := valid
	future.Version, future.IssuedAt, future.ExpiresAt = "agent-task-credential-v1", now.Add(2*time.Minute), now.Add(3*time.Minute)
	if _, err := service.Verify(t.Context(), signedCredentialClaims(t, service, future), validScope); apperror.CodeOf(err) != "agent.credential.expired" {
		t.Fatalf("future issue=%v", err)
	}
	if _, err := service.Verify(t.Context(), token, AgentTaskCredentialScope{WorkspaceID: valid.WorkspaceID, ProcessID: valid.ProcessID, TaskRunID: valid.TaskRunID, Tool: ""}); err != nil {
		t.Fatalf("empty tool=%v", err)
	}
}

func signedCredentialClaims(t *testing.T, service *AgentTaskCredentialApplicationService, claims AgentTaskCredentialClaims) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	return signedCredentialPayload(service, base64.RawURLEncoding.EncodeToString(payload))
}

func signedCredentialPayload(service *AgentTaskCredentialApplicationService, payload string) string {
	return payload + "." + base64.RawURLEncoding.EncodeToString(service.signature(payload))
}
