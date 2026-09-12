package upload

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
)

const fileDownloadTicketVersion = "v1"
const fileDownloadTicketLifetime = 2 * time.Minute

type FileDownloadTicketClaims struct {
	WorkspaceID           string `json:"workspace_id"`
	UserID                string `json:"user_id"`
	AuthorizationRevision string `json:"authorization_revision,omitempty"`
	FileID                string `json:"file_id"`
	ObjectKey             string `json:"object_key"`
	RecordID              string `json:"record_id"`
	FieldKey              string `json:"field_key"`
	ExpiresAtUnix         int64  `json:"expires_at_unix"`
}

type FileDownloadTicketService struct {
	key   []byte
	clock func() time.Time
}

func NewFileDownloadTicketService(key []byte, clock func() time.Time) (*FileDownloadTicketService, error) {
	if len(key) < 32 {
		return nil, errors.New("file download ticket signing key is required")
	}
	if clock == nil {
		clock = time.Now
	}
	return &FileDownloadTicketService{key: append([]byte(nil), key...), clock: clock}, nil
}

func (s *FileDownloadTicketService) Issue(ctx context.Context, workspaceID, userID, authorizationRevision string, request runtimeext.FileDownloadRequest) (runtimeext.FileDownloadTicket, error) {
	if err := ctx.Err(); err != nil {
		return runtimeext.FileDownloadTicket{}, err
	}
	claims := FileDownloadTicketClaims{
		WorkspaceID: strings.TrimSpace(workspaceID), UserID: strings.TrimSpace(userID), AuthorizationRevision: strings.TrimSpace(authorizationRevision),
		FileID: strings.TrimSpace(request.FileID), ObjectKey: strings.TrimSpace(request.Binding.ObjectKey), RecordID: strings.TrimSpace(request.Binding.RecordID),
		FieldKey: strings.TrimSpace(request.Binding.FileIDField), ExpiresAtUnix: s.clock().UTC().Add(fileDownloadTicketLifetime).Unix(),
	}
	if !claims.valid() {
		return runtimeext.FileDownloadTicket{}, apperror.New(apperror.KindBadRequest, "backend.upload.download_ticket_request_invalid", nil, nil)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return runtimeext.FileDownloadTicket{}, err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, s.key)
	_, _ = mac.Write([]byte(fileDownloadTicketVersion + "." + encoded))
	token := fileDownloadTicketVersion + "." + encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	path := "/uploads/" + url.PathEscape(claims.FileID) + "?download_ticket=" + url.QueryEscape(token)
	return runtimeext.FileDownloadTicket{ProtectedDownload: path, ExpiresAt: time.Unix(claims.ExpiresAtUnix, 0).UTC()}, nil
}

func (s *FileDownloadTicketService) Authorize(ctx context.Context, token, workspaceID, userID, authorizationRevision, fileID string) (FileDownloadTicketClaims, error) {
	if err := ctx.Err(); err != nil {
		return FileDownloadTicketClaims{}, err
	}
	if s == nil || len(s.key) < 32 {
		return FileDownloadTicketClaims{}, apperror.New(apperror.KindUnavailable, "backend.upload.download_ticket_unavailable", nil, nil)
	}
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 3 || parts[0] != fileDownloadTicketVersion {
		return FileDownloadTicketClaims{}, invalidFileDownloadTicket()
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return FileDownloadTicketClaims{}, invalidFileDownloadTicket()
	}
	mac := hmac.New(sha256.New, s.key)
	_, _ = mac.Write([]byte(parts[0] + "." + parts[1]))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return FileDownloadTicketClaims{}, invalidFileDownloadTicket()
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return FileDownloadTicketClaims{}, invalidFileDownloadTicket()
	}
	var claims FileDownloadTicketClaims
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&claims); err != nil || !claims.valid() {
		return FileDownloadTicketClaims{}, invalidFileDownloadTicket()
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return FileDownloadTicketClaims{}, invalidFileDownloadTicket()
	}
	now := s.clock().UTC().Unix()
	if claims.ExpiresAtUnix <= now || claims.ExpiresAtUnix > now+int64(fileDownloadTicketLifetime/time.Second) ||
		claims.WorkspaceID != strings.TrimSpace(workspaceID) || claims.UserID != strings.TrimSpace(userID) ||
		claims.AuthorizationRevision != strings.TrimSpace(authorizationRevision) || claims.FileID != strings.TrimSpace(fileID) {
		return FileDownloadTicketClaims{}, invalidFileDownloadTicket()
	}
	return claims, nil
}

func (claims FileDownloadTicketClaims) valid() bool {
	return claims.WorkspaceID != "" && claims.UserID != "" && claims.FileID != "" && claims.ObjectKey != "" && claims.RecordID != "" && claims.FieldKey != "" && claims.ExpiresAtUnix > 0
}

func invalidFileDownloadTicket() error {
	return apperror.New(apperror.KindForbidden, "backend.upload.download_ticket_invalid", nil, nil)
}
