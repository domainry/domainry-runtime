package integrationtest

import (
	"net/http"
	"testing"
	"time"
)

// Live acceptance needs to distinguish upstream HTTP failures from local
// protocol validation. Do not record URLs, headers, credentials or bodies.
type businessLiveModelTransport struct{ t *testing.T }

func (d businessLiveModelTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	started := time.Now()
	response, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		d.t.Logf("Live model transport failed (%T), elapsed=%s", err, time.Since(started).Round(time.Millisecond))
	} else {
		d.t.Logf("Live model HTTP status=%d, elapsed=%s", response.StatusCode, time.Since(started).Round(time.Millisecond))
	}
	return response, err
}
