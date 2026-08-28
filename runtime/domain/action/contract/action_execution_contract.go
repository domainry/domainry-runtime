package contract

// ActionExecutionStore is the minimal execution persistence capability needed
// by the Action runtime.
type ActionExecutionStore interface {
	ActionExecutionClaimStore
}
