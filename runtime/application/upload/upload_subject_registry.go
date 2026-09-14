package upload

import (
	"context"
	"github.com/domainry/domainry-foundation/apperror"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"strings"
)

// UploadSubjectBinding is a host fact recorded from the authenticated upload,
// independently of editable business records and scanner receipts.
type UploadSubjectBinding struct {
	WorkspaceID, FileID, Filename, ObjectKey, FieldKey, UserID, SHA256 string
}

type UploadSubjectStore interface {
	InsertUploadSubject(context.Context, UploadSubjectBinding) error
	FindUploadSubject(context.Context, string, string) (UploadSubjectBinding, error)
}

type UploadSubjectRegistry struct{ store UploadSubjectStore }

func NewUploadSubjectRegistry(store UploadSubjectStore) *UploadSubjectRegistry {
	return &UploadSubjectRegistry{store: store}
}

func (s *UploadSubjectRegistry) Register(ctx context.Context, artifact lifecyclecontract.UploadArtifact, principal principalmodel.Principal) error {
	if err := uploadAuthorizePrincipal(principal); err != nil {
		return err
	}
	if s == nil || s.store == nil || principal.UserID == "" || artifact.WorkspaceID != principal.WorkspaceID {
		return uploadAccessError(apperror.KindForbidden, "backend.upload.subject_binding_denied")
	}
	return s.store.InsertUploadSubject(ctx, UploadSubjectBinding{WorkspaceID: artifact.WorkspaceID, FileID: artifact.ID, Filename: artifact.Filename, ObjectKey: artifact.ObjectKey, FieldKey: artifact.FieldKey, UserID: principal.UserID, SHA256: artifact.SHA256})
}

func (s *UploadSubjectRegistry) Authorize(ctx context.Context, workspace, user, identifier string) error {
	if s == nil || s.store == nil || strings.TrimSpace(workspace) == "" || strings.TrimSpace(user) == "" {
		return uploadAccessError(apperror.KindForbidden, "backend.upload.subject_binding_denied")
	}
	binding, err := s.store.FindUploadSubject(ctx, workspace, identifier)
	if err != nil || binding.WorkspaceID != workspace || binding.UserID != user || (binding.FileID != identifier && binding.Filename != identifier) {
		return uploadAccessError(apperror.KindForbidden, "backend.upload.subject_binding_denied")
	}
	return nil
}

type uploadClaimPrincipalKey struct{}

// WithUploadClaimPrincipal is used by the trusted Action executor when an
// Action claims an upload. Read-bound opens and tickets use record authorization.
func WithUploadClaimPrincipal(ctx context.Context, principal principalmodel.Principal) context.Context {
	return context.WithValue(ctx, uploadClaimPrincipalKey{}, principal)
}
