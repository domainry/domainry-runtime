package database

import "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/timevalue"

// MarshalTimeJSON is the single durable JSON boundary for Runtime-owned data.
// Absolute instants are stored as UTC Unix-millisecond numbers.
func MarshalTimeJSON(value any) ([]byte, error) {
	return timevalue.MarshalJSON(value)
}

// UnmarshalTimeJSON restores Runtime domain transport types while rejecting
// string-encoded instants in durable state.
func UnmarshalTimeJSON(raw []byte, destination any) error {
	return timevalue.UnmarshalJSON(raw, destination)
}
