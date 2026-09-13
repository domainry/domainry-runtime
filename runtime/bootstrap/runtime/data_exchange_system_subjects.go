package runtime

import (
	"context"
	"encoding/json"
	"fmt"

	sdk "github.com/domainry/domainry-data-exchange-sdk"
	"github.com/domainry/domainry-foundation/requestcontext"
	"github.com/domainry/domainry-lifecycle-sdk/contract"
	"github.com/domainry/domainry-lifecycle-sdk/model"
)

type dataExchangeSubjectLifecycle struct{ binding sdk.Binding }

func (dataExchangeSubjectLifecycle) Owner(context.Context) string { return "data_exchange" }
func (s dataExchangeSubjectLifecycle) subjects() (sdk.SubjectLifecycle, error) {
	binding, ok := s.binding.(sdk.SubjectLifecycleBinding)
	if !ok || binding.SubjectLifecycle() == nil {
		return nil, fmt.Errorf("Data Exchange subject lifecycle unavailable")
	}
	return binding.SubjectLifecycle(), nil
}
func (s dataExchangeSubjectLifecycle) PreviewSubject(ctx context.Context, workspace, subject string) (json.RawMessage, error) {
	binding, err := s.subjects()
	if err != nil {
		return nil, err
	}
	return binding.PreviewSubject(requestcontext.WithWorkspaceID(ctx, workspace), workspace, subject)
}
func (s dataExchangeSubjectLifecycle) ExportSubjectForRequest(ctx context.Context, _ string, workspace, subject string) (json.RawMessage, error) {
	binding, err := s.subjects()
	if err != nil {
		return nil, err
	}
	return binding.ExportSubject(requestcontext.WithWorkspaceID(ctx, workspace), workspace, subject)
}
func (s dataExchangeSubjectLifecycle) EraseSubjectForRequest(context.Context, string, string, string, []lifecyclemodel.LegalHold) (json.RawMessage, error) {
	return nil, fmt.Errorf("Data Exchange erasure requires a persisted plan")
}
func (s dataExchangeSubjectLifecycle) PrepareSubjectErasure(ctx context.Context, request, workspace, subject string) (json.RawMessage, error) {
	binding, err := s.subjects()
	if err != nil {
		return nil, err
	}
	return binding.PrepareSubjectErasure(requestcontext.WithWorkspaceID(ctx, workspace), sdk.SubjectErasureRequest{RequestID: request, WorkspaceID: workspace, SubjectID: subject})
}
func (s dataExchangeSubjectLifecycle) ErasePreparedSubject(ctx context.Context, request, workspace, subject string, plan json.RawMessage, holds []lifecyclemodel.LegalHold) (json.RawMessage, error) {
	binding, err := s.subjects()
	if err != nil {
		return nil, err
	}
	evidence, err := json.Marshal(holds)
	if err != nil {
		return nil, err
	}
	return binding.ErasePreparedSubject(requestcontext.WithWorkspaceID(ctx, workspace), sdk.SubjectErasureRequest{RequestID: request, WorkspaceID: workspace, SubjectID: subject, LegalHolds: evidence}, plan)
}

var _ contract.SubjectExecutionHandler = dataExchangeSubjectLifecycle{}
var _ contract.PreparedSubjectErasureHandler = dataExchangeSubjectLifecycle{}
