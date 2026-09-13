package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/domainry/domainry-foundation/requestcontext"
	sdk "github.com/domainry/domainry-integration-sdk"
	"github.com/domainry/domainry-lifecycle-sdk/contract"
	lifecyclemodel "github.com/domainry/domainry-lifecycle-sdk/model"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/subjectevidence"
)

type integrationSubjectLifecycle struct {
	subjects sdk.SubjectLifecycle
	evidence *subjectevidence.Handler
}

func (integrationSubjectLifecycle) Owner(context.Context) string { return "integration" }
func (s integrationSubjectLifecycle) request(ctx context.Context, id, workspace, subject string, prepare bool) (sdk.SubjectErasureRequest, error) {
	if s.subjects == nil || s.evidence == nil {
		return sdk.SubjectErasureRequest{}, fmt.Errorf("Integration subject lifecycle unavailable")
	}
	var p subjectevidence.SubjectProvenance
	var err error
	if prepare {
		p, err = s.evidence.PreparedSubjectProvenance(ctx, id, workspace, subject)
	} else {
		p, err = s.evidence.SubjectProvenance(ctx, workspace, subject)
	}
	if err != nil {
		return sdk.SubjectErasureRequest{}, err
	}
	r := sdk.SubjectErasureRequest{WorkspaceID: workspace, SubjectID: subject, RequestID: id, EventIDs: p.EventIDs, PublicationMessageIDs: p.PublicationMessageIDs}
	for _, ref := range p.Resources {
		r.Resources = append(r.Resources, sdk.SubjectRecordReference{ObjectKey: ref.ObjectKey, RecordID: ref.RecordID})
	}
	return r, nil
}
func (s integrationSubjectLifecycle) PreviewSubject(ctx context.Context, workspace, subject string) (json.RawMessage, error) {
	r, err := s.request(ctx, "", workspace, subject, false)
	if err != nil {
		return nil, err
	}
	return s.subjects.PreviewSubject(requestcontext.WithWorkspaceID(ctx, workspace), r)
}
func (s integrationSubjectLifecycle) ExportSubjectForRequest(ctx context.Context, id, workspace, subject string) (json.RawMessage, error) {
	r, err := s.request(ctx, id, workspace, subject, false)
	if err != nil {
		return nil, err
	}
	return s.subjects.ExportSubject(requestcontext.WithWorkspaceID(ctx, workspace), r)
}
func (s integrationSubjectLifecycle) EraseSubjectForRequest(context.Context, string, string, string, []lifecyclemodel.LegalHold) (json.RawMessage, error) {
	return nil, fmt.Errorf("Integration erasure requires persisted plan")
}
func (s integrationSubjectLifecycle) PrepareSubjectErasure(ctx context.Context, id, workspace, subject string) (json.RawMessage, error) {
	r, err := s.request(ctx, id, workspace, subject, true)
	if err != nil {
		return nil, err
	}
	return s.subjects.PrepareSubjectErasure(requestcontext.WithWorkspaceID(ctx, workspace), r)
}
func (s integrationSubjectLifecycle) ErasePreparedSubject(ctx context.Context, id, workspace, subject string, p json.RawMessage, holds []lifecyclemodel.LegalHold) (json.RawMessage, error) {
	r, err := s.request(ctx, id, workspace, subject, true)
	if err != nil {
		return nil, err
	}
	r.LegalHolds, err = json.Marshal(holds)
	if err != nil {
		return nil, err
	}
	return s.subjects.ErasePreparedSubject(requestcontext.WithWorkspaceID(ctx, workspace), r, p)
}

var _ contract.SubjectExecutionHandler = integrationSubjectLifecycle{}
var _ contract.PreparedSubjectErasureHandler = integrationSubjectLifecycle{}
