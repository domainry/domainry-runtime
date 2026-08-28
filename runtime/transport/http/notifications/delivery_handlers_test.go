package notifications

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	notificationcontract "github.com/domainry/domainry-runtime/runtime/domain/notification/contract"
	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

func notificationHTTPDeliveryPolicy() notificationmodel.NotificationDeliveryPolicy {
	return notificationmodel.NotificationDeliveryPolicy{
		Enabled: true, QuietStart: "22:00", QuietEnd: "07:00", Timezone: "UTC",
		MaxPerRecipientPerHour: 10, DedupeWindowSeconds: 60, FallbackChannels: []string{"email"},
	}
}

func TestNotificationDeliveryMetricsHandler(t *testing.T) {
	t.Run("permission", func(t *testing.T) {
		repo := &notificationHTTPRepository{}
		handler, response, principal := newNotificationHTTPHandler(repo)
		accessfixture.Set(principal, accessfixture.Bundle{})
		handler.metrics(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/notifications/metrics", nil))
		if response.status != http.StatusForbidden || response.code != "auth.permission_denied" || repo.lastSince != "" {
			t.Fatalf("response=%+v since=%q", response, repo.lastSince)
		}
	})

	for _, test := range []struct {
		name      string
		query     string
		wantHours int
	}{
		{name: "default", wantHours: 24},
		{name: "invalid", query: "?hours=bad", wantHours: 24},
		{name: "capped", query: "?hours=999", wantHours: 720},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := &notificationHTTPRepository{metrics: notificationmodel.NotificationDeliveryMetrics{Summary: notificationmodel.NotificationDeliveryMetricBucket{Total: 7}}}
			handler, response, _ := newNotificationHTTPHandler(repo)
			before := time.Now().UTC().Add(-time.Duration(test.wantHours) * time.Hour)
			handler.metrics(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/notifications/metrics"+test.query, nil))
			after := time.Now().UTC().Add(-time.Duration(test.wantHours) * time.Hour)
			since, err := time.Parse(time.RFC3339, repo.lastSince)
			if err != nil || since.Before(before.Add(-time.Second)) || since.After(after.Add(time.Second)) {
				t.Fatalf("since=%q err=%v want between %s and %s", repo.lastSince, err, before, after)
			}
			if repo.lastWorkspaceID != "workspace-1" || response.status != http.StatusOK {
				t.Fatalf("workspace=%q response=%+v", repo.lastWorkspaceID, response)
			}
			metrics, ok := response.value.(notificationmodel.NotificationDeliveryMetrics)
			if !ok || metrics.Summary.Total != 7 {
				t.Fatalf("metrics=%#v", response.value)
			}
		})
	}

	t.Run("repository error", func(t *testing.T) {
		failure := errors.New("metrics unavailable")
		handler, response, _ := newNotificationHTTPHandler(&notificationHTTPRepository{err: failure})
		handler.metrics(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/notifications/metrics?hours=1", nil))
		if !errors.Is(response.err, failure) {
			t.Fatalf("error=%v", response.err)
		}
	})
}

func TestNotificationGovernanceHandlers(t *testing.T) {
	t.Run("catalog permission and success", func(t *testing.T) {
		handler, response, principal := newNotificationHTTPHandler(&notificationHTTPRepository{})
		accessfixture.Set(principal, accessfixture.Bundle{})
		handler.governanceCatalog(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/notifications/governance/catalog", nil))
		if response.status != http.StatusForbidden {
			t.Fatalf("permission response=%+v", response)
		}
		handler, response, _ = newNotificationHTTPHandler(&notificationHTTPRepository{})
		handler.governanceCatalog(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/notifications/governance/catalog", nil))
		value, ok := response.value.(notificationmodel.NotificationGovernanceCatalog)
		if response.status != http.StatusOK || !ok || len(value.EventTypes) != 1 || value.EventTypes[0].Key != "test.event" {
			t.Fatalf("catalog=%#v response=%+v", response.value, response)
		}
	})
	t.Run("catalog repository error", func(t *testing.T) {
		handler, response, principal := newNotificationHTTPHandler(&notificationHTTPRepository{})
		principal.WorkspaceID = ""
		handler.governanceCatalog(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/notifications/governance/catalog", nil))
		if response.err == nil {
			t.Fatal("unscoped catalog request was accepted")
		}
	})

	for _, test := range []struct {
		name      string
		query     string
		wantHours int
	}{{"default", "", 24}, {"invalid", "?hours=bad", 24}, {"capped", "?hours=999", 720}} {
		t.Run("inbox metrics "+test.name, func(t *testing.T) {
			repo := &notificationHTTPRepository{inboxMetrics: notificationmodel.NotificationInboxGovernanceMetrics{Summary: notificationmodel.NotificationInboxAggregate{Items: 9}}}
			handler, response, principal := newNotificationHTTPHandler(repo)
			accessfixture.Set(principal, accessfixture.Bundle{Permissions: []string{notificationcontract.PermissionPolicyRead}})
			before := time.Now().UTC().Add(-time.Duration(test.wantHours) * time.Hour)
			handler.inboxGovernanceMetrics(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/notifications/governance/inbox-metrics"+test.query, nil))
			after := time.Now().UTC().Add(-time.Duration(test.wantHours) * time.Hour)
			since, err := time.Parse(time.RFC3339, repo.lastSince)
			if err != nil || since.Before(before.Add(-time.Second)) || since.After(after.Add(time.Second)) || response.status != http.StatusOK || repo.lastWorkspaceID != "workspace-1" {
				t.Fatalf("since=%q err=%v response=%+v", repo.lastSince, err, response)
			}
		})
	}

	t.Run("inbox metrics permission and repository error", func(t *testing.T) {
		handler, response, principal := newNotificationHTTPHandler(&notificationHTTPRepository{})
		accessfixture.Set(principal, accessfixture.Bundle{})
		handler.inboxGovernanceMetrics(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/notifications/governance/inbox-metrics", nil))
		if response.status != http.StatusForbidden {
			t.Fatalf("permission response=%+v", response)
		}
		failure := errors.New("inbox metrics unavailable")
		handler, response, principal = newNotificationHTTPHandler(&notificationHTTPRepository{err: failure})
		accessfixture.Set(principal, accessfixture.Bundle{Permissions: []string{notificationcontract.PermissionPolicyRead}})
		handler.inboxGovernanceMetrics(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/notifications/governance/inbox-metrics", nil))
		if !errors.Is(response.err, failure) {
			t.Fatalf("error=%v", response.err)
		}
	})
}

func TestNotificationDeliveryPolicyHandlers(t *testing.T) {
	t.Run("get permission", func(t *testing.T) {
		repo := &notificationHTTPRepository{}
		handler, response, principal := newNotificationHTTPHandler(repo)
		accessfixture.Set(principal, accessfixture.Bundle{})
		handler.getPolicy(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/notifications/policy", nil))
		if response.status != http.StatusForbidden || response.code != "auth.permission_denied" {
			t.Fatalf("response=%+v", response)
		}
	})

	t.Run("get success", func(t *testing.T) {
		policy := notificationHTTPDeliveryPolicy()
		repo := &notificationHTTPRepository{policy: policy}
		handler, response, _ := newNotificationHTTPHandler(repo)
		handler.getPolicy(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/notifications/policy", nil))
		got, ok := response.value.(notificationmodel.NotificationDeliveryPolicy)
		if response.status != http.StatusOK || !ok || got.MaxPerRecipientPerHour != policy.MaxPerRecipientPerHour {
			t.Fatalf("response=%+v scope=%+v", response, repo.lastScope)
		}
	})

	t.Run("get error", func(t *testing.T) {
		failure := errors.New("policy unavailable")
		handler, response, _ := newNotificationHTTPHandler(&notificationHTTPRepository{err: failure})
		handler.getPolicy(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/notifications/policy", nil))
		if !errors.Is(response.err, failure) {
			t.Fatalf("error=%v", response.err)
		}
	})

	t.Run("save permission and decode", func(t *testing.T) {
		handler, response, principal := newNotificationHTTPHandler(&notificationHTTPRepository{})
		accessfixture.Set(principal, accessfixture.Bundle{Permissions: []string{notificationcontract.PermissionPolicyRead}})
		handler.savePolicy(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/notifications/policy", strings.NewReader(`{}`)))
		if response.status != http.StatusForbidden {
			t.Fatalf("permission response=%+v", response)
		}

		handler, response, _ = newNotificationHTTPHandler(&notificationHTTPRepository{})
		handler.savePolicy(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/notifications/policy", strings.NewReader(`{`)))
		if response.status != http.StatusBadRequest {
			t.Fatalf("decode response=%+v", response)
		}
	})

	t.Run("save validation success and repository error", func(t *testing.T) {
		handler, response, _ := newNotificationHTTPHandler(&notificationHTTPRepository{})
		handler.savePolicy(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/notifications/policy", strings.NewReader(`{"timezone":"UTC","quiet_start":"22:00","quiet_end":"07:00"}`)))
		if apperror.CodeOf(response.err) != "backend.notification.policy_frequency_invalid" {
			t.Fatalf("validation error=%v", response.err)
		}

		policy := notificationHTTPDeliveryPolicy()
		repo := &notificationHTTPRepository{}
		handler, response, _ = newNotificationHTTPHandler(repo)
		handler.savePolicy(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/notifications/policy", strings.NewReader(deliveryPolicyJSON(policy))))
		saved, ok := response.value.(notificationmodel.NotificationDeliveryPolicy)
		if response.status != http.StatusOK || !ok || saved.UpdatedBy != "reviewer" || saved.UpdatedAt == "" {
			t.Fatalf("response=%+v saved=%+v scope=%+v", response, saved, repo.lastScope)
		}

		failure := errors.New("save policy failed")
		handler, response, _ = newNotificationHTTPHandler(&notificationHTTPRepository{err: failure})
		handler.savePolicy(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/notifications/policy", strings.NewReader(deliveryPolicyJSON(policy))))
		if !errors.Is(response.err, failure) {
			t.Fatalf("error=%v", response.err)
		}
	})
}

func TestNotificationRecipientPreferenceHandlers(t *testing.T) {
	t.Run("list permission success and error", func(t *testing.T) {
		handler, response, principal := newNotificationHTTPHandler(&notificationHTTPRepository{})
		accessfixture.Set(principal, accessfixture.Bundle{})
		handler.listPreferences(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/notifications/preferences", nil))
		if response.status != http.StatusForbidden {
			t.Fatalf("permission response=%+v", response)
		}

		repo := &notificationHTTPRepository{preferences: []notificationmodel.NotificationRecipientPreference{{RecipientKey: "user-1"}}}
		handler, response, _ = newNotificationHTTPHandler(repo)
		handler.listPreferences(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/notifications/preferences", nil))
		value, ok := response.value.(map[string]any)
		if response.status != http.StatusOK || !ok || value["count"] != 1 || repo.lastWorkspaceID != "workspace-1" {
			t.Fatalf("response=%+v workspace=%q", response, repo.lastWorkspaceID)
		}

		failure := errors.New("preferences unavailable")
		handler, response, _ = newNotificationHTTPHandler(&notificationHTTPRepository{err: failure})
		handler.listPreferences(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/notifications/preferences", nil))
		if !errors.Is(response.err, failure) {
			t.Fatalf("error=%v", response.err)
		}
	})

	t.Run("save permission decode validation success and error", func(t *testing.T) {
		handler, response, principal := newNotificationHTTPHandler(&notificationHTTPRepository{})
		accessfixture.Set(principal, accessfixture.Bundle{Permissions: []string{notificationcontract.PermissionPolicyRead}})
		handler.savePreference(httptest.NewRecorder(), preferenceRequest(`{}`, "user-1"))
		if response.status != http.StatusForbidden {
			t.Fatalf("permission response=%+v", response)
		}

		handler, response, _ = newNotificationHTTPHandler(&notificationHTTPRepository{})
		handler.savePreference(httptest.NewRecorder(), preferenceRequest(`{`, "user-1"))
		if response.status != http.StatusBadRequest {
			t.Fatalf("decode response=%+v", response)
		}

		handler, response, _ = newNotificationHTTPHandler(&notificationHTTPRepository{})
		handler.savePreference(httptest.NewRecorder(), preferenceRequest(`{}`, ""))
		if apperror.CodeOf(response.err) != "backend.notification.preference_identity_required" {
			t.Fatalf("validation error=%v", response.err)
		}

		repo := &notificationHTTPRepository{}
		handler, response, _ = newNotificationHTTPHandler(repo)
		handler.savePreference(httptest.NewRecorder(), preferenceRequest(`{"recipient_key":"spoofed"}`, "user-1"))
		saved, ok := response.value.(notificationmodel.NotificationRecipientPreference)
		if response.status != http.StatusOK || !ok || saved.RecipientKey != "user-1" || saved.EnabledChannels == nil || saved.UpdatedBy != "reviewer" || repo.lastWorkspaceID != "workspace-1" {
			t.Fatalf("response=%+v saved=%+v workspace=%q", response, saved, repo.lastWorkspaceID)
		}

		failure := errors.New("save preference failed")
		handler, response, _ = newNotificationHTTPHandler(&notificationHTTPRepository{err: failure})
		handler.savePreference(httptest.NewRecorder(), preferenceRequest(`{}`, "user-1"))
		if !errors.Is(response.err, failure) {
			t.Fatalf("error=%v", response.err)
		}
	})
}

func preferenceRequest(body, recipientKey string) *http.Request {
	request := httptest.NewRequest(http.MethodPut, "/notifications/preferences/"+recipientKey, strings.NewReader(body))
	request.SetPathValue("recipientKey", recipientKey)
	return request
}

func deliveryPolicyJSON(value notificationmodel.NotificationDeliveryPolicy) string {
	return `{"enabled":true,"quiet_start":"` + value.QuietStart + `","quiet_end":"` + value.QuietEnd + `","timezone":"` + value.Timezone + `","max_per_recipient_per_hour":10,"dedupe_window_seconds":60,"fallback_channels":["email"]}`
}
