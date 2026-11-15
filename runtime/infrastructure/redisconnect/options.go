// Package redisconnect owns Redis client TLS option construction shared by the
// principal-cache and distributed rate-limit adapters.
package redisconnect

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strings"

	redis "github.com/redis/go-redis/v9"
)

func ParseURL(rawURL, caCertFile, component string) (*redis.Options, error) {
	options, err := redis.ParseURL(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, err
	}
	tlsConfig, err := withRootCA(options.TLSConfig, caCertFile, component)
	if err != nil {
		return nil, err
	}
	options.TLSConfig = tlsConfig
	return options, nil
}

func ParseClusterURL(rawURL, caCertFile, component string) (*redis.ClusterOptions, error) {
	options, err := redis.ParseClusterURL(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, err
	}
	tlsConfig, err := withRootCA(options.TLSConfig, caCertFile, component)
	if err != nil {
		return nil, err
	}
	options.TLSConfig = tlsConfig
	return options, nil
}

func withRootCA(current *tls.Config, caCertFile, component string) (*tls.Config, error) {
	caCertFile = strings.TrimSpace(caCertFile)
	if caCertFile == "" {
		return current, nil
	}
	if current == nil {
		return nil, fmt.Errorf("%s Redis CA certificate requires a rediss:// URL", component)
	}
	pem, err := os.ReadFile(caCertFile)
	if err != nil {
		return nil, fmt.Errorf("read %s Redis CA certificate: %w", component, err)
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		roots = x509.NewCertPool()
	}
	if !roots.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("%s Redis CA certificate contains no valid PEM certificate", component)
	}
	configured := current.Clone()
	configured.RootCAs = roots
	return configured, nil
}
