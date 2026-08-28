package notification

import (
	"context"
	"errors"
	"testing"

	sourcenotification "github.com/domainry/domainry-notification"
	sourceinbox "github.com/domainry/domainry-notification/inbox"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func TestNotificationAudienceResolverRegistryRegistrationFreezeAndResolution(t *testing.T) {
	var nilRegistry *NotificationAudienceResolverRegistry
	if nilRegistry.Register("owner", func(context.Context, sourceinbox.Event) ([]sourcenotification.UserID, error) { return nil, nil }) || nilRegistry.Register("owner", nil) {
		t.Fatal("nil registry accepted registration")
	}
	if _, err := nilRegistry.ResolveAudience(t.Context(), "owner", sourceinbox.Event{}); apperror.CodeOf(err) != "backend.notification.inbox_audience_resolver_unavailable" {
		t.Fatalf("nil resolve err=%v", err)
	}
	registry := NewNotificationAudienceResolverRegistry()
	if registry.Register("", func(context.Context, sourceinbox.Event) ([]sourcenotification.UserID, error) { return nil, nil }) || registry.Register("owner", nil) {
		t.Fatal("invalid resolver registration accepted")
	}
	wantErr := errors.New("directory unavailable")
	if !registry.Register(" owner ", func(_ context.Context, event sourceinbox.Event) ([]sourcenotification.UserID, error) {
		if event.SubjectID == "fail" {
			return nil, wantErr
		}
		return []sourcenotification.UserID{"owner-1"}, nil
	}) || registry.Register("owner", func(context.Context, sourceinbox.Event) ([]sourcenotification.UserID, error) { return nil, nil }) {
		t.Fatal("duplicate registration contract failed")
	}
	values, err := registry.ResolveAudience(t.Context(), " owner ", sourceinbox.Event{SubjectID: "ok"})
	if err != nil || len(values) != 1 || values[0] != "owner-1" {
		t.Fatalf("values=%v err=%v", values, err)
	}
	if _, err := registry.ResolveAudience(t.Context(), "missing", sourceinbox.Event{}); apperror.CodeOf(err) != "backend.notification.inbox_audience_resolver_unavailable" {
		t.Fatalf("missing err=%v", err)
	}
	if _, err := registry.ResolveAudience(t.Context(), "owner", sourceinbox.Event{SubjectID: "fail"}); !errors.Is(err, wantErr) {
		t.Fatalf("resolver error=%v", err)
	}
	registry.Freeze()
	registry.Freeze()
	if registry.Register("late", func(context.Context, sourceinbox.Event) ([]sourcenotification.UserID, error) { return nil, nil }) {
		t.Fatal("frozen registry accepted registration")
	}
	nilRegistry.Freeze()
}
