package operationsmodel

import "time"

const OperationsSystemPurposeRuntimeControl = "runtime_control"

type OperationsControlKind string

const (
	OperationsControlMaintenance   OperationsControlKind = "maintenance"
	OperationsControlWorkerPause   OperationsControlKind = "worker_pause"
	OperationsControlInstanceDrain OperationsControlKind = "instance_drain"
)

type OperationsControlState string

const (
	OperationsControlInactive OperationsControlState = "inactive"
	OperationsControlActive   OperationsControlState = "active"
)

// OperationsControl is desired operational state stored in the shared
// database. Every Runtime instance reconstructs its local admission state from
// these records; the row is not a process-local toggle.
type OperationsControl struct {
	SystemPurpose string                 `json:"system_purpose"`
	Kind          OperationsControlKind  `json:"kind"`
	Owner         string                 `json:"owner"`
	State         OperationsControlState `json:"state"`
	Reason        string                 `json:"reason"`
	Reference     string                 `json:"reference,omitempty"`
	UpdatedBy     string                 `json:"updated_by"`
	Revision      int64                  `json:"revision"`
	UpdatedAt     time.Time              `json:"updated_at"`
}

func (control OperationsControl) Active() bool { return control.State == OperationsControlActive }
