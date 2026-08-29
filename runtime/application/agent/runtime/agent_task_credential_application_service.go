package runtime

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type AgentTaskCredentialClaims struct {
	Version      string                             `json:"version"`
	WorkspaceID  string                             `json:"workspace_id"`
	ProcessID    string                             `json:"process_id,omitempty"`
	TaskRunID    string                             `json:"task_run_id"`
	Principal    agentmodel.AgentPrincipalReference `json:"principal"`
	AllowedTools []string                           `json:"allowed_tools"`
	IssuedAt     time.Time                          `json:"issued_at"`
	ExpiresAt    time.Time                          `json:"expires_at"`
	Nonce        string                             `json:"nonce"`
}

type AgentTaskCredentialScope struct {
	WorkspaceID string
	ProcessID   string
	TaskRunID   string
	Tool        string
}

type AgentTaskCredentialApplicationService struct {
	key   []byte
	clock workerplatform.Clock
	ids   workerplatform.IdentifierGenerator
}

func NewAgentTaskCredentialApplicationService(key []byte, clock workerplatform.Clock, ids workerplatform.IdentifierGenerator) *AgentTaskCredentialApplicationService {
	if clock == nil {
		clock = workerplatform.SystemClock{}
	}
	if ids == nil {
		ids = workerplatform.CryptoIdentifierGenerator{}
	}
	return &AgentTaskCredentialApplicationService{key: append([]byte(nil), key...), clock: clock, ids: ids}
}

func (s *AgentTaskCredentialApplicationService) Issue(ctx context.Context, claims AgentTaskCredentialClaims, ttl time.Duration) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if s == nil || len(s.key) < 32 {
		return "", apperror.New(apperror.KindUnavailable, "agent.credential.signer_unavailable", nil, nil)
	}
	claims.Version = "agent-task-credential-v1"
	claims.WorkspaceID, claims.ProcessID, claims.TaskRunID = strings.TrimSpace(claims.WorkspaceID), strings.TrimSpace(claims.ProcessID), strings.TrimSpace(claims.TaskRunID)
	claims.AllowedTools = agentUniqueStrings(claims.AllowedTools)
	_, workspaceErr := principalmodel.NewWorkspaceID(claims.WorkspaceID)
	if workspaceErr != nil {
		return "", apperror.New(apperror.KindBadRequest, "agent.credential.claims_invalid", nil, nil)
	}
	if claims.TaskRunID == "" || claims.Principal.UserID == "" || claims.Principal.RoleKey == "" || ttl <= 0 || ttl > 15*time.Minute {
		return "", apperror.New(apperror.KindBadRequest, "agent.credential.claims_invalid", nil, nil)
	}
	now := s.clock.Now().UTC()
	claims.IssuedAt, claims.ExpiresAt = now, now.Add(ttl)
	if strings.TrimSpace(claims.Nonce) == "" {
		claims.Nonce = "agent_credential_" + s.ids.NewID()
	}
	payload, _ := json.Marshal(claims)
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	return encoded + "." + base64.RawURLEncoding.EncodeToString(s.signature(encoded)), nil
}

func (s *AgentTaskCredentialApplicationService) Verify(ctx context.Context, token string, scope AgentTaskCredentialScope) (AgentTaskCredentialClaims, error) {
	if err := ctx.Err(); err != nil {
		return AgentTaskCredentialClaims{}, err
	}
	if s == nil || len(s.key) < 32 {
		return AgentTaskCredentialClaims{}, apperror.New(apperror.KindUnavailable, "agent.credential.signer_unavailable", nil, nil)
	}
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 2 {
		return AgentTaskCredentialClaims{}, agentCredentialDenied("agent.credential.malformed")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(signature, s.signature(parts[0])) {
		return AgentTaskCredentialClaims{}, agentCredentialDenied("agent.credential.signature_invalid")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return AgentTaskCredentialClaims{}, agentCredentialDenied("agent.credential.malformed")
	}
	var claims AgentTaskCredentialClaims
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Version != "agent-task-credential-v1" {
		return AgentTaskCredentialClaims{}, agentCredentialDenied("agent.credential.version_invalid")
	}
	now := s.clock.Now().UTC()
	if !claims.ExpiresAt.After(now) || claims.IssuedAt.After(now.Add(time.Minute)) {
		return AgentTaskCredentialClaims{}, agentCredentialDenied("agent.credential.expired")
	}
	if claims.WorkspaceID != strings.TrimSpace(scope.WorkspaceID) || claims.ProcessID != strings.TrimSpace(scope.ProcessID) || claims.TaskRunID != strings.TrimSpace(scope.TaskRunID) {
		return AgentTaskCredentialClaims{}, agentCredentialDenied("agent.credential.scope_denied")
	}
	if tool := strings.TrimSpace(scope.Tool); tool != "" && !agentContains(claims.AllowedTools, tool) {
		return AgentTaskCredentialClaims{}, agentCredentialDenied("agent.credential.tool_denied")
	}
	return claims, nil
}

func (s *AgentTaskCredentialApplicationService) signature(payload string) []byte {
	mac := hmac.New(sha256.New, s.key)
	_, _ = mac.Write([]byte(payload))
	return mac.Sum(nil)
}

func agentCredentialDenied(code string) error {
	return apperror.New(apperror.KindForbidden, code, nil, nil)
}
