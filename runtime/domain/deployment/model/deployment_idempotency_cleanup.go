package deploymentmodel

import "time"

type IdempotencyCleanupRequest struct {
	LeaseOwner string
	LeaseTTL   time.Duration
	BatchSize  int
	Now        time.Time
}

type IdempotencyCleanupResult struct {
	Acquired     bool   `json:"acquired"`
	LeaseOwner   string `json:"lease_owner,omitempty"`
	FencingToken int64  `json:"fencing_token,omitempty"`
	Deleted      int    `json:"deleted"`
	StartedAt    string `json:"started_at,omitempty"`
	CompletedAt  string `json:"completed_at,omitempty"`
}

type IdempotencyOperationalStatus struct {
	Backlog              map[string]int           `json:"backlog"`
	BacklogTotal         int                      `json:"backlog_total"`
	Conflicts            int64                    `json:"conflicts"`
	ExpiredLeases        int                      `json:"expired_leases"`
	Acquired             int64                    `json:"acquired"`
	Replayed             int64                    `json:"replayed"`
	LeaseLost            int64                    `json:"lease_lost"`
	DuplicateSideEffects int64                    `json:"duplicate_side_effects"`
	Cleanup              IdempotencyCleanupStatus `json:"cleanup"`
	Alerts               []IdempotencyAlert       `json:"alerts"`
}

type IdempotencyAlert struct {
	Key       string  `json:"key"`
	Severity  string  `json:"severity"`
	Status    string  `json:"status"`
	Value     float64 `json:"value"`
	Threshold float64 `json:"threshold"`
	Unit      string  `json:"unit"`
}

func (status *IdempotencyOperationalStatus) EvaluateAlerts() {
	status.Alerts = []IdempotencyAlert{
		idempotencyThresholdAlert("duplicate_side_effect", "critical", float64(status.DuplicateSideEffects), 1, "count"),
		idempotencyThresholdAlert("lease_lost_spike", "warning", float64(status.LeaseLost), 5, "count"),
		idempotencyThresholdAlert("processing_timeout", "warning", float64(status.ExpiredLeases), 1, "count"),
	}
	ratio := float64(status.Replayed)
	if status.Acquired > 0 {
		ratio /= float64(status.Acquired)
	}
	replay := idempotencyThresholdAlert("replay_rate_high", "warning", ratio, 5, "replay_per_acquire")
	if status.Replayed+status.Acquired < 100 {
		replay.Status = "insufficient_data"
	}
	status.Alerts = append(status.Alerts, replay)
}

func idempotencyThresholdAlert(key, severity string, value, threshold float64, unit string) IdempotencyAlert {
	state := "ok"
	if value >= threshold {
		state = "firing"
	}
	return IdempotencyAlert{Key: key, Severity: severity, Status: state, Value: value, Threshold: threshold, Unit: unit}
}

type IdempotencyCleanupStatus struct {
	State           string `json:"state"`
	LeaseOwner      string `json:"lease_owner,omitempty"`
	LeaseExpiresAt  string `json:"lease_expires_at,omitempty"`
	FencingToken    int64  `json:"fencing_token"`
	LastStartedAt   string `json:"last_started_at,omitempty"`
	LastCompletedAt string `json:"last_completed_at,omitempty"`
	LastDeleted     int    `json:"last_deleted"`
	LastError       string `json:"last_error,omitempty"`
}
