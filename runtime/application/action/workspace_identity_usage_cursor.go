package action

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
)

const (
	workspaceIdentityUsageCursorVersion = 1
	workspaceIdentityUsageCursorTTL     = 15 * time.Minute
)

type WorkspaceIdentityUsageCursorBinding struct {
	ActionKey             string
	WorkspaceID           string
	SubjectID             string
	AuthorizationRevision string
}

type WorkspaceIdentityUsageCursorCodec interface {
	Seal(string, WorkspaceIdentityUsageCursorBinding) (string, error)
	Open(string, WorkspaceIdentityUsageCursorBinding) (string, error)
}

type workspaceIdentityUsageCursorCodec struct {
	aead           cipher.AEAD
	installationID string
	now            func() time.Time
}

type workspaceIdentityUsageCursorEnvelope struct {
	Version               int    `json:"v"`
	IdentityCursor        string `json:"cursor"`
	ActionKey             string `json:"action"`
	WorkspaceID           string `json:"workspace"`
	SubjectID             string `json:"subject"`
	AuthorizationRevision string `json:"authorization_revision"`
	InstallationID        string `json:"installation"`
	ExpiresAt             int64  `json:"expires_at"`
}

func NewWorkspaceIdentityUsageCursorCodec(secret []byte, installationID string, now func() time.Time) (WorkspaceIdentityUsageCursorCodec, error) {
	installationID = strings.TrimSpace(installationID)
	if len(secret) < 16 || installationID == "" {
		return nil, errors.New("Workspace identity usage cursor key and installation are required")
	}
	keyMaterial := append([]byte("domainry-runtime/workspace-identity-usage-cursor/v1\x00"), secret...)
	key := sha256.Sum256(keyMaterial)
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if now == nil {
		now = time.Now
	}
	return &workspaceIdentityUsageCursorCodec{aead: aead, installationID: installationID, now: now}, nil
}

func (codec *workspaceIdentityUsageCursorCodec) Seal(identityCursor string, binding WorkspaceIdentityUsageCursorBinding) (string, error) {
	identityCursor = strings.TrimSpace(identityCursor)
	if codec == nil || codec.aead == nil || identityCursor == "" || !binding.valid() {
		return "", errors.New("Workspace identity usage cursor cannot be sealed")
	}
	envelope := workspaceIdentityUsageCursorEnvelope{
		Version: workspaceIdentityUsageCursorVersion, IdentityCursor: identityCursor,
		ActionKey: strings.TrimSpace(binding.ActionKey), WorkspaceID: strings.TrimSpace(binding.WorkspaceID), SubjectID: strings.TrimSpace(binding.SubjectID),
		AuthorizationRevision: strings.TrimSpace(binding.AuthorizationRevision), InstallationID: codec.installationID,
		ExpiresAt: codec.now().UTC().Add(workspaceIdentityUsageCursorTTL).Unix(),
	}
	plaintext, err := json.Marshal(envelope)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, codec.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := codec.aead.Seal(nonce, nonce, plaintext, []byte("workspace-identity-usage-cursor-v1"))
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (codec *workspaceIdentityUsageCursorCodec) Open(cursor string, binding WorkspaceIdentityUsageCursorBinding) (string, error) {
	cursor = strings.TrimSpace(cursor)
	if codec == nil || codec.aead == nil || cursor == "" || !binding.valid() {
		return "", errors.New("Workspace identity usage cursor is invalid")
	}
	sealed, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || base64.RawURLEncoding.EncodeToString(sealed) != cursor || len(sealed) <= codec.aead.NonceSize() {
		return "", errors.New("Workspace identity usage cursor is invalid")
	}
	nonce, ciphertext := sealed[:codec.aead.NonceSize()], sealed[codec.aead.NonceSize():]
	plaintext, err := codec.aead.Open(nil, nonce, ciphertext, []byte("workspace-identity-usage-cursor-v1"))
	if err != nil {
		return "", errors.New("Workspace identity usage cursor is invalid")
	}
	var envelope workspaceIdentityUsageCursorEnvelope
	if err := json.Unmarshal(plaintext, &envelope); err != nil || envelope.Version != workspaceIdentityUsageCursorVersion || strings.TrimSpace(envelope.IdentityCursor) == "" ||
		envelope.ActionKey != strings.TrimSpace(binding.ActionKey) || envelope.WorkspaceID != strings.TrimSpace(binding.WorkspaceID) || envelope.SubjectID != strings.TrimSpace(binding.SubjectID) ||
		envelope.AuthorizationRevision != strings.TrimSpace(binding.AuthorizationRevision) || envelope.InstallationID != codec.installationID || envelope.ExpiresAt <= codec.now().UTC().Unix() {
		return "", errors.New("Workspace identity usage cursor is invalid")
	}
	return envelope.IdentityCursor, nil
}

func (binding WorkspaceIdentityUsageCursorBinding) valid() bool {
	return strings.TrimSpace(binding.ActionKey) != "" && strings.TrimSpace(binding.WorkspaceID) != "" && strings.TrimSpace(binding.SubjectID) != "" && strings.TrimSpace(binding.AuthorizationRevision) != ""
}
