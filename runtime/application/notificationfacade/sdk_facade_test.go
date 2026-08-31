package notificationfacade

import (
	"errors"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	notificationsdk "github.com/domainry/domainry-notification-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

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

func TestApplicationUserAuthorityUsesAdministrationChannel(t *testing.T) {
	ctx := identitysdk.WithRequestIdentity(t.Context(), identitysdk.RequestIdentity{Principal: identitysdk.Principal{Known: true}, AccessToken: "token"})
	value, err := (&NotificationApplicationService{}).user(ctx, principalmodel.Principal{})
	if err != nil {
		t.Fatal(err)
	}
	if value.Surface != "administration" {
		t.Fatalf("surface=%q", value.Surface)
	}
}

func TestMapErrorPreservesStableNotificationCodeAndRetryKind(t *testing.T) {
	source := &notificationsdk.Error{StatusCode: 503, Code: "notification.remote_unavailable", Retryable: true}
	mapped := mapError(source)
	if apperror.KindOf(mapped) != apperror.KindUnavailable || apperror.CodeOf(mapped) != "backend.notification.remote_unavailable" || !errors.Is(mapped, source) {
		t.Fatalf("mapped=%v kind=%s code=%s", mapped, apperror.KindOf(mapped), apperror.CodeOf(mapped))
	}
}
