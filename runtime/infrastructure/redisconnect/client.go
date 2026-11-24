package redisconnect

import (
	"crypto/tls"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	redis "github.com/redis/go-redis/v9"
)

// NewClient builds a standalone or Sentinel-backed Redis client. The authority
// in a rediss+sentinel URL identifies the Redis TLS certificate. Sentinel seeds
// and discovered Redis addresses must serve a certificate for that authority.
func NewClient(rawURL, caCertFile, component string, timeout time.Duration) (redis.UniversalClient, error) {
	scheme, _, _ := strings.Cut(strings.TrimSpace(rawURL), ":")
	if strings.EqualFold(scheme, "rediss+sentinel") {
		options, err := parseSentinelURL(rawURL, caCertFile, component, timeout)
		if err != nil {
			return nil, err
		}
		return redis.NewFailoverClient(options), nil
	}
	options, err := ParseURL(rawURL, caCertFile, component)
	if err != nil {
		return nil, err
	}
	options.DialTimeout = timeout
	options.ReadTimeout = timeout
	options.WriteTimeout = timeout
	options.ContextTimeoutEnabled = true
	return redis.NewClient(options), nil
}

func parseSentinelURL(rawURL, caCertFile, component string, timeout time.Duration) (*redis.FailoverOptions, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Scheme != "rediss+sentinel" || parsed.Hostname() == "" || parsed.Fragment != "" {
		return nil, errors.New("invalid Redis Sentinel URL")
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return nil, errors.New("invalid Redis Sentinel URL query")
	}
	for key, values := range query {
		switch key {
		case "sentinel_addr":
		case "master_name", "sentinel_username", "sentinel_password":
			if len(values) != 1 {
				return nil, errors.New("repeated Redis Sentinel URL option")
			}
		default:
			return nil, errors.New("unknown Redis Sentinel URL option")
		}
	}
	master := strings.TrimSpace(query.Get("master_name"))
	seeds := query["sentinel_addr"]
	if master == "" || len(seeds) == 0 {
		return nil, errors.New("Redis Sentinel URL requires master_name and sentinel_addr")
	}
	seen := make(map[string]struct{}, len(seeds))
	for _, seed := range seeds {
		host, port, splitErr := net.SplitHostPort(seed)
		portNumber, portErr := strconv.Atoi(port)
		if splitErr != nil || portErr != nil || host == "" || portNumber < 1 || portNumber > 65535 ||
			strings.ContainsAny(host, " \t\r\n/@?#") {
			return nil, errors.New("Redis Sentinel seed must be a host and port")
		}
		canonical := net.JoinHostPort(strings.ToLower(host), strconv.Itoa(portNumber))
		if _, exists := seen[canonical]; exists {
			return nil, errors.New("duplicate Redis Sentinel seed address")
		}
		seen[canonical] = struct{}{}
	}
	if query.Get("sentinel_username") != "" && query.Get("sentinel_password") == "" {
		return nil, errors.New("Redis Sentinel username requires a Sentinel password")
	}

	parsed.Scheme = "rediss"
	parsed.RawQuery = ""
	data, err := redis.ParseURL(parsed.String())
	if err != nil {
		return nil, errors.New("invalid Redis Sentinel data endpoint or database")
	}
	tlsConfig, err := withRootCA(data.TLSConfig, caCertFile, component)
	if err != nil {
		return nil, err
	}
	dialer := &tls.Dialer{NetDialer: &net.Dialer{Timeout: timeout}, Config: tlsConfig}
	// The pool applies DialTimeout to the entire failover dial. Reserve time for
	// the two cached-Sentinel lookups and another seed after a silent failure.
	// Each individual TCP/TLS connection and Redis command remains bounded.
	discoveryTimeout := time.Duration(1<<63 - 1)
	if timeout <= discoveryTimeout/3 {
		discoveryTimeout = 3 * timeout
	}

	return &redis.FailoverOptions{
		MasterName:            master,
		SentinelAddrs:         seeds,
		SentinelUsername:      query.Get("sentinel_username"),
		SentinelPassword:      query.Get("sentinel_password"),
		Username:              data.Username,
		Password:              data.Password,
		DB:                    data.DB,
		TLSConfig:             tlsConfig,
		Dialer:                dialer.DialContext,
		DialTimeout:           discoveryTimeout,
		ReadTimeout:           timeout,
		WriteTimeout:          timeout,
		DialerRetries:         1,
		ContextTimeoutEnabled: true,
		// A timed out command may already have been written to the old master.
		MaxRetries: -1,
	}, nil
}
