package operationsmodel

import "time"

type OperationsLeaseCount struct {
	Owner   string `json:"owner"`
	Live    int64  `json:"live"`
	Expired int64  `json:"expired"`
}

type OperationsLeaseSnapshot struct {
	InstanceID string                 `json:"instance_id"`
	CheckedAt  time.Time              `json:"checked_at"`
	Live       int64                  `json:"live"`
	Expired    int64                  `json:"expired"`
	Owners     []OperationsLeaseCount `json:"owners"`
}
