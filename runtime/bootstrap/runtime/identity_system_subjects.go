package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/domainry/domainry-foundation/requestcontext"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	lifecyclemodel "github.com/domainry/domainry-lifecycle-sdk/model"
)

type identitySystemSubjectLifecycle struct{ binding identitysdk.Binding }

func (identitySystemSubjectLifecycle) Owner(context.Context) string { return "identity" }
func (value identitySystemSubjectLifecycle) subjects() (identitysdk.SystemSubjects, error) {
	binding, ok := value.binding.(identitysdk.SystemSubjectBinding)
	if !ok || binding.SystemSubjects() == nil {
		return nil, fmt.Errorf("Identity subject lifecycle is unavailable")
	}
	return binding.SystemSubjects(), nil
}
func (value identitySystemSubjectLifecycle) PreviewSubject(ctx context.Context, workspaceID, subjectID string) (json.RawMessage, error) {
	subjects, err := value.subjects()
	if err != nil {
		return nil, err
	}
	return subjects.PreviewSubject(requestcontext.WithWorkspaceID(ctx, workspaceID), workspaceID, subjectID)
}
func (value identitySystemSubjectLifecycle) ExportSubject(ctx context.Context, workspaceID, subjectID string) (json.RawMessage, error) {
	subjects, err := value.subjects()
	if err != nil {
		return nil, err
	}
	return subjects.ExportSubject(requestcontext.WithWorkspaceID(ctx, workspaceID), workspaceID, subjectID)
}
func (value identitySystemSubjectLifecycle) ExportSubjectForRequest(ctx context.Context, _ string, workspaceID, subjectID string) (json.RawMessage, error) {
	return value.ExportSubject(ctx, workspaceID, subjectID)
}
func (value identitySystemSubjectLifecycle) EraseSubject(ctx context.Context, workspaceID, subjectID string, holds []lifecyclemodel.LegalHold) (json.RawMessage, error) {
	return value.EraseSubjectForRequest(ctx, "subject:"+subjectID, workspaceID, subjectID, holds)
}
func (value identitySystemSubjectLifecycle) EraseSubjectForRequest(ctx context.Context, requestID, workspaceID, subjectID string, holds []lifecyclemodel.LegalHold) (json.RawMessage, error) {
	subjects, err := value.subjects()
	if err != nil {
		return nil, err
	}
	evidence, err := json.Marshal(holds)
	if err != nil {
		return nil, err
	}
	return subjects.EraseSubjectForRequest(requestcontext.WithWorkspaceID(ctx, workspaceID), identitysdk.SubjectErasureRequest{WorkspaceID: workspaceID, SubjectID: subjectID, RequestID: requestID, LegalHolds: evidence})
}

var _ lifecyclecontract.SubjectExecutionHandler = identitySystemSubjectLifecycle{}
