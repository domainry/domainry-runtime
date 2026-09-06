package workspaceprovision

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

const workspaceAdministrationCursorTTL = 15 * time.Minute

type WorkspaceAdministrationCursorBinding struct {
	ActionKey             string
	SubjectID             string
	AuthorizationRevision string
}

type WorkspaceAdministrationCursorCodec interface {
	Seal(string, WorkspaceAdministrationCursorBinding) (string, error)
	Open(string, WorkspaceAdministrationCursorBinding) (string, error)
}

type workspaceAdministrationCursorCodec struct {
	aead           cipher.AEAD
	installationID string
	now            func() time.Time
}

type workspaceAdministrationCursorEnvelope struct {
	Version               int    `json:"v"`
	AfterCanonicalCode    string `json:"after"`
	ActionKey             string `json:"action"`
	SubjectID             string `json:"subject"`
	AuthorizationRevision string `json:"authorization_revision"`
	InstallationID        string `json:"installation"`
	ExpiresAt             int64  `json:"expires_at"`
}

func NewWorkspaceAdministrationCursorCodec(secret []byte, installationID string, now func() time.Time) (WorkspaceAdministrationCursorCodec, error) {
	installationID = strings.TrimSpace(installationID)
	if len(secret) < 16 || installationID == "" {
		return nil, errors.New("Workspace administration cursor key and installation are required")
	}
	key := sha256.Sum256(append([]byte("domainry-runtime/workspace-administration-cursor/v1\x00"), secret...))
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
	return &workspaceAdministrationCursorCodec{aead: aead, installationID: installationID, now: now}, nil
}

func (codec *workspaceAdministrationCursorCodec) Seal(after string, binding WorkspaceAdministrationCursorBinding) (string, error) {
	after = strings.TrimSpace(after)
	if codec == nil || codec.aead == nil || after == "" || !binding.valid() {
		return "", errors.New("Workspace administration cursor cannot be sealed")
	}
	payload, err := json.Marshal(workspaceAdministrationCursorEnvelope{
		Version: 1, AfterCanonicalCode: after, ActionKey: strings.TrimSpace(binding.ActionKey),
		SubjectID: strings.TrimSpace(binding.SubjectID), AuthorizationRevision: strings.TrimSpace(binding.AuthorizationRevision),
		InstallationID: codec.installationID, ExpiresAt: codec.now().UTC().Add(workspaceAdministrationCursorTTL).Unix(),
	})
	if err != nil {
		return "", err
	}
	nonce := make([]byte, codec.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(codec.aead.Seal(nonce, nonce, payload, []byte("workspace-administration-cursor-v1"))), nil
}

func (codec *workspaceAdministrationCursorCodec) Open(cursor string, binding WorkspaceAdministrationCursorBinding) (string, error) {
	cursor = strings.TrimSpace(cursor)
	if codec == nil || codec.aead == nil || cursor == "" || !binding.valid() {
		return "", errors.New("Workspace administration cursor is invalid")
	}
	sealed, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || base64.RawURLEncoding.EncodeToString(sealed) != cursor || len(sealed) <= codec.aead.NonceSize() {
		return "", errors.New("Workspace administration cursor is invalid")
	}
	plaintext, err := codec.aead.Open(nil, sealed[:codec.aead.NonceSize()], sealed[codec.aead.NonceSize():], []byte("workspace-administration-cursor-v1"))
	if err != nil {
		return "", errors.New("Workspace administration cursor is invalid")
	}
	var envelope workspaceAdministrationCursorEnvelope
	if json.Unmarshal(plaintext, &envelope) != nil || envelope.Version != 1 || strings.TrimSpace(envelope.AfterCanonicalCode) == "" ||
		envelope.ActionKey != strings.TrimSpace(binding.ActionKey) || envelope.SubjectID != strings.TrimSpace(binding.SubjectID) ||
		envelope.AuthorizationRevision != strings.TrimSpace(binding.AuthorizationRevision) || envelope.InstallationID != codec.installationID ||
		envelope.ExpiresAt <= codec.now().UTC().Unix() {
		return "", errors.New("Workspace administration cursor is invalid")
	}
	return envelope.AfterCanonicalCode, nil
}

func (binding WorkspaceAdministrationCursorBinding) valid() bool {
	return strings.TrimSpace(binding.ActionKey) != "" && strings.TrimSpace(binding.SubjectID) != "" && strings.TrimSpace(binding.AuthorizationRevision) != ""
}
