package record

import "time"

// The record store persists timestamps as Unix milliseconds. Return that same
// precision to callers, while advancing the revision for consecutive writes
// inside one millisecond so optimistic updates cannot reuse a prior version.
func recordCanonicalMutationTimestamp(now time.Time, previous string) string {
	version := now.UTC().Truncate(time.Millisecond)
	if prior, err := time.Parse(time.RFC3339Nano, previous); err == nil {
		prior = prior.UTC().Truncate(time.Millisecond)
		if !version.After(prior) {
			version = prior.Add(time.Millisecond)
		}
	}
	return version.Format(time.RFC3339Nano)
}
