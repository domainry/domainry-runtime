package runtime

import "testing"

func TestIntegrationRetryBackoffIsBoundedAndJittered(t *testing.T) {
	first := IntegrationRetryDelaySecondsFor("task-a", 2)
	second := IntegrationRetryDelaySecondsFor("task-b", 2)
	if first < 240 || first > 360 || second < 240 || second > 360 {
		t.Fatalf("jitter outside 20%% bound: %d %d", first, second)
	}
	if first == second {
		t.Fatalf("task cohort received identical retry delay: %d", first)
	}
	if got := IntegrationRetryDelaySecondsFor("task", 99); got < 2880 || got > 3600 {
		t.Fatalf("max delay not bounded: %d", got)
	}
}
