package config

import "testing"

func TestCapacityConfigurationRejectsUnsafeCombinations(t *testing.T) {
	tests := []struct {
		name   string
		values map[string]string
	}{
		{"workspace exceeds global", map[string]string{"CAPACITY_GLOBAL_IN_FLIGHT": "8", "CAPACITY_WORKSPACE_IN_FLIGHT": "9"}},
		{"retry consumes global", map[string]string{"CAPACITY_GLOBAL_IN_FLIGHT": "8", "CAPACITY_RETRY_IN_FLIGHT": "8"}},
		{"invalid hysteresis", map[string]string{"CAPACITY_DEGRADED_RATIO": "0.5", "CAPACITY_RECOVERY_RATIO": "0.8"}},
		{"unbounded headers", map[string]string{"HTTP_MAX_HEADER_BYTES": "8388608"}},
		{"workspace rate exceeds global", map[string]string{"CAPACITY_GLOBAL_RATE_PER_MINUTE": "100", "CAPACITY_WORKSPACE_RATE_PER_MINUTE": "101"}},
		{"connector provider exceeds global", map[string]string{"CAPACITY_CONNECTOR_GLOBAL_IN_FLIGHT": "4", "CAPACITY_CONNECTOR_PROVIDER_IN_FLIGHT": "5"}},
		{"connector workspace rate exceeds global", map[string]string{"CAPACITY_CONNECTOR_GLOBAL_RATE_PER_MINUTE": "100", "CAPACITY_CONNECTOR_WORKSPACE_RATE_PER_MINUTE": "101"}},
		{"invalid queue threshold", map[string]string{"CAPACITY_QUEUE_DEPTH_THRESHOLD": "0"}},
		{"workspace batch queue exceeds global", map[string]string{"CAPACITY_BATCH_JOB_QUEUE_LIMIT": "10", "CAPACITY_BATCH_JOB_WORKSPACE_QUEUE_LIMIT": "11"}},
		{"event workspace exceeds global", map[string]string{"BUSINESS_EVENT_GLOBAL_CONNECTIONS": "8", "BUSINESS_EVENT_WORKSPACE_CONNECTIONS": "9"}},
		{"event principal exceeds workspace", map[string]string{"BUSINESS_EVENT_WORKSPACE_CONNECTIONS": "8", "BUSINESS_EVENT_PRINCIPAL_CONNECTIONS": "9"}},
		{"event replay unbounded", map[string]string{"BUSINESS_EVENT_REPLAY_LIMIT": "100001"}},
		{"event heartbeat too fast", map[string]string{"BUSINESS_EVENT_HEARTBEAT_INTERVAL": "100ms"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := LoadContract(Source{Name: "test", Values: test.values}); err == nil {
				t.Fatalf("unsafe capacity configuration accepted: %#v", test.values)
			}
		})
	}
}
