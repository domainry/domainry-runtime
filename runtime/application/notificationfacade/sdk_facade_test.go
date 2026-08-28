package notificationfacade

import (
	"errors"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	notificationsdk "github.com/domainry/domainry-notification-sdk"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	notificationhttp "github.com/domainry/domainry-runtime/runtime/transport/http/notifications"
)

var _ notificationhttp.NotificationApplication = (*Service)(nil)

func TestAuthorityUsesOriginalIdentityRequestToken(t *testing.T) {
	ctx := identitysdk.WithRequestIdentity(t.Context(), identitysdk.RequestIdentity{Principal: identitysdk.Principal{Known: true}, AccessToken: " bearer-token "})
	value, err := authority(ctx, " business_workspace ")
	if err != nil {
		t.Fatal(err)
	}
	if value.AccessToken != " bearer-token " || value.Surface != "business_workspace" {
		t.Fatalf("authority=%+v", value)
	}
	if _, err := authority(t.Context(), "business_workspace"); apperror.KindOf(err) != apperror.KindForbidden {
		t.Fatalf("missing token error=%v", err)
	}
}

func TestMapErrorPreservesStableNotificationCodeAndRetryKind(t *testing.T) {
	source := &notificationsdk.Error{StatusCode: 503, Code: "notification.remote_unavailable", Retryable: true}
	mapped := mapError(source)
	if apperror.KindOf(mapped) != apperror.KindUnavailable || apperror.CodeOf(mapped) != "backend.notification.remote_unavailable" || !errors.Is(mapped, source) {
		t.Fatalf("mapped=%v kind=%s code=%s", mapped, apperror.KindOf(mapped), apperror.CodeOf(mapped))
	}
}
