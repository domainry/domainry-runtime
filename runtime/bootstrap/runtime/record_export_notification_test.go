package runtime

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/requestcontext"
	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	"github.com/domainry/domainry-notification-sdk/modulehost"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	"github.com/domainry/domainry-runtime/testsupport/notificationsdkfixture"
)

func TestRecordExportNotificationUsesClaimedWorkspace(t *testing.T) {
	store, err := persistence.OpenContext(t.Context(), config.Config{
		DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "runtime.db"), IntegrationSecretKey: "record-export-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := notificationsdkfixture.BindTransactions(store); err != nil {
		t.Fatal(err)
	}
	const sourceEvent = "record-export:job-fixture:completed"
	compile := func(intent notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error) {
		return notificationmodel.NotificationEvent{
			EventID: "export-" + intent.WorkspaceID, WorkspaceID: intent.WorkspaceID, Source: "records",
			SourceEventID: intent.SourceEventID, RecipientUserIDs: intent.RecipientUserIDs, Status: "pending",
		}, nil
	}
	publish := recordExportNotificationPublisher(store, compile, func(context.Context, notificationmodel.NotificationIntent, principalmodel.SystemScope) (notificationmodel.NotificationEvent, bool, error) {
		t.Fatal("embedded exports must use the host transaction publisher")
		return notificationmodel.NotificationEvent{}, false, nil
	})
	for _, workspace := range []string{"business-a", "business-b"} {
		ctx := requestcontext.WithActorID(requestcontext.WithWorkspaceID(t.Context(), workspace), "actor-"+workspace)
		intent := notificationmodel.NotificationIntent{WorkspaceID: workspace, SourceEventID: sourceEvent, RecipientUserIDs: []string{"actor-" + workspace}}
		if err := publish(ctx, intent); err != nil {
			t.Fatalf("workspace %s: %v", workspace, err)
		}
		if err := publish(ctx, intent); err != nil {
			t.Fatalf("replay in workspace %s: %v", workspace, err)
		}
		committed, err := store.NotificationTransactions().EventCommitted(ctx, modulehost.EventIdentity{WorkspaceID: workspace, Source: "records", SourceEventID: sourceEvent})
		if err != nil || !committed {
			t.Fatalf("workspace %s committed=%v err=%v", workspace, committed, err)
		}
	}

	ctx := requestcontext.WithActorID(requestcontext.WithWorkspaceID(t.Context(), "business-a"), "actor-business-a")
	for _, intent := range []notificationmodel.NotificationIntent{
		{WorkspaceID: "business-b", SourceEventID: "forged-workspace", RecipientUserIDs: []string{"actor-business-a"}},
		{WorkspaceID: "business-a", SourceEventID: "forged-recipient", RecipientUserIDs: []string{"actor-business-b"}},
	} {
		if err := publish(ctx, intent); apperror.CodeOf(err) != "backend.notification.application_scope_mismatch" {
			t.Fatalf("scope mismatch %s: %v", intent.SourceEventID, err)
		}
		committed, err := store.NotificationTransactions().EventCommitted(ctx, modulehost.EventIdentity{WorkspaceID: intent.WorkspaceID, Source: "records", SourceEventID: intent.SourceEventID})
		if err != nil || committed {
			t.Fatalf("rejected intent %s committed=%v err=%v", intent.SourceEventID, committed, err)
		}
	}
}
