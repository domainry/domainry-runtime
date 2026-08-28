package runtime

import "testing"

func TestIntegrationRetryDelayScheduleAndUpperBound(t *testing.T) {
	for _, test := range []struct {
		attempt int
		want    int
	}{
		{attempt: -1, want: 60},
		{attempt: 1, want: 60},
		{attempt: 2, want: 300},
		{attempt: 3, want: 900},
		{attempt: 4, want: 3600},
	} {
		if got := IntegrationRetryDelaySeconds(test.attempt); got != test.want {
			t.Fatalf("attempt=%d delay=%d want=%d", test.attempt, got, test.want)
		}
	}
	foundUpperClamp := false
	for index := 0; index < 1000; index++ {
		if IntegrationRetryDelaySecondsFor(string(rune(index)), 4) == 3600 {
			foundUpperClamp = true
			break
		}
	}
	if !foundUpperClamp {
		t.Fatal("no deterministic task exercised retry upper bound")
	}
}

func TestIntegrationWorkerPrincipal(t *testing.T) {
	if principal := IntegrationWorkerPrincipal("   "); principal.Known || principal.WorkspaceID != "" {
		t.Fatalf("blank workspace principal=%#v", principal)
	}
	principal := IntegrationWorkerPrincipal(" workspace-a ")
	if !principal.Known || principal.WorkspaceID != "workspace-a" || principal.UserID != "integration:worker" || !principal.SystemScope.Valid() {
		t.Fatalf("principal=%#v", principal)
	}
	if !principal.HasPermission("integration.dispatch") || !principal.HasPermission("any.explicit.system.capability") {
		t.Fatalf("system capabilities=%#v", principal.SystemCapabilities)
	}
}

func TestIntegrationSanitizeKeyAndShortHash(t *testing.T) {
	for _, test := range []struct {
		value string
		want  string
	}{
		{value: "", want: "integration"},
		{value: " CRM-API / V2 ", want: "crm_api___v2"},
		{value: "___", want: "integration"},
		{value: "Already_safe123", want: "already_safe123"},
		{value: "客户", want: "integration"},
	} {
		if got := IntegrationSanitizeKey(test.value); got != test.want {
			t.Fatalf("value=%q sanitized=%q want=%q", test.value, got, test.want)
		}
	}
	if got := IntegrationShortHash("runtime"); got != "d92c6a81b2ff5009" {
		t.Fatalf("hash=%q", got)
	}
	if IntegrationShortHash("runtime") == IntegrationShortHash("Runtime") {
		t.Fatal("short hash lost case-sensitive input identity")
	}
}
