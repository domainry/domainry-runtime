package runtimeext

import (
	"strings"
	"time"
	_ "time/tzdata"
)

// ApplicationTimeZoneExecution exposes application policy captured once for
// this Action execution. It never reads the browser or the host's local zone.
// Optional capability discovery preserves older ActionExecution adapters.
type ApplicationTimeZoneExecution interface {
	ApplicationTimeZone() (string, error)
}

func ResolveApplicationTimeZone(execution ActionExecution) (string, error) {
	provider, ok := execution.(ApplicationTimeZoneExecution)
	if !ok {
		return "", &BusinessError{Code: "backend.action.application_time_zone_unavailable"}
	}
	zone, err := provider.ApplicationTimeZone()
	if err != nil {
		return "", err
	}
	if err := ValidateApplicationTimeZone(zone); err != nil {
		return "", err
	}
	return zone, nil
}

// A missing value is unavailable, not UTC. Runtime applies the manifest's UTC
// default before capturing this contract, so old adapters cannot silently pick
// a different accounting policy.
func ValidateApplicationTimeZone(zone string) error {
	if zone == "" {
		return &BusinessError{Code: "backend.action.application_time_zone_unavailable"}
	}
	if zone != strings.TrimSpace(zone) || zone == "Local" || strings.HasPrefix(zone, "/") || strings.HasPrefix(zone, ".") || zone != "UTC" && !strings.Contains(zone, "/") {
		return &BusinessError{Code: "backend.action.application_time_zone_invalid"}
	}
	if _, err := time.LoadLocation(zone); err != nil {
		return &BusinessError{Code: "backend.action.application_time_zone_invalid", Message: err.Error()}
	}
	return nil
}
