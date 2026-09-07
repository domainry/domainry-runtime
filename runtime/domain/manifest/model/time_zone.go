package manifestmodel

import (
	"fmt"
	"strings"
	"time"
	_ "time/tzdata"
)

// EffectiveTimeZone preserves the UTC behavior of older installations. This is
// application metadata, never the process-local zone or a request parameter.
func (manifest ManifestSchema) EffectiveTimeZone() string {
	if manifest.TimeZone == "" {
		return "UTC"
	}
	return manifest.TimeZone
}

func (manifest ManifestSchema) ValidateTimeZone() error {
	zone := manifest.EffectiveTimeZone()
	if zone == "Local" || zone != strings.TrimSpace(zone) || zone != "UTC" && !strings.Contains(zone, "/") {
		return fmt.Errorf("time_zone %q must be a canonical IANA name such as Asia/Tokyo or UTC", zone)
	}
	if _, err := time.LoadLocation(zone); err != nil {
		return fmt.Errorf("cannot load time_zone %q: %w", zone, err)
	}
	return nil
}
