package runtime

import (
	"context"
	"encoding/json"
	"fmt"

	lifecyclemodel "github.com/domainry/domainry-lifecycle-sdk/model"
	notificationsdk "github.com/domainry/domainry-notification-sdk"
)

type notificationSystemSubjectLifecycle struct {
	subjects notificationsdk.SystemSubjects
}

func (notificationSystemSubjectLifecycle) Owner(context.Context) string { return "notification" }

func (s notificationSystemSubjectLifecycle) PreviewSubject(ctx context.Context, workspaceID, subjectID string) (json.RawMessage, error) {
	if s.subjects == nil {
		return nil, fmt.Errorf("Notification system subject port is unavailable")
	}
	return s.subjects.PreviewSubject(ctx, workspaceID, subjectID)
}

func (s notificationSystemSubjectLifecycle) ExportSubject(ctx context.Context, workspaceID, subjectID string) (json.RawMessage, error) {
	if s.subjects == nil {
		return nil, fmt.Errorf("Notification system subject port is unavailable")
	}
	return s.subjects.ExportSubject(ctx, workspaceID, subjectID)
}

func (s notificationSystemSubjectLifecycle) EraseSubject(ctx context.Context, workspaceID, subjectID string, holds []lifecyclemodel.LegalHold) (json.RawMessage, error) {
	if s.subjects == nil {
		return nil, fmt.Errorf("Notification system subject port is unavailable")
	}
	evidence, err := json.Marshal(holds)
	if err != nil {
		return nil, err
	}
	return s.subjects.EraseSubject(ctx, workspaceID, subjectID, evidence)
}
