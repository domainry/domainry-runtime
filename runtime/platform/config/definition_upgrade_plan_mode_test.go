package config

import "testing"

func TestDefinitionUpgradePlanModeRequestedReadsTheRawEnvironment(t *testing.T) {
	for _, testCase := range []struct {
		value string
		want  bool
	}{
		{"plan", true},
		{" PLAN ", true},
		{"apply", false},
		{"verify", false},
		{"", false},
	} {
		t.Setenv("DEFINITION_UPGRADE_MODE", testCase.value)
		if got := DefinitionUpgradePlanModeRequested(); got != testCase.want {
			t.Fatalf("DEFINITION_UPGRADE_MODE=%q requested=%v want %v", testCase.value, got, testCase.want)
		}
	}
}
