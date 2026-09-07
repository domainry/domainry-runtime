package runtimehost

// Runtime hosts must interpret application-selected IANA zones even when the
// deployment image has no system zoneinfo files. This embeds the lookup data;
// it does not select a default zone or change time.Local.
import _ "time/tzdata"
