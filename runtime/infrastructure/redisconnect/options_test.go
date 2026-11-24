package redisconnect

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseURLLoadsPrivateRootCAWithoutDisablingVerification(t *testing.T) {
	caFile, certificate := writeTestCA(t)
	options, err := ParseURL("rediss://redis.internal:6379/0", caFile, "principal-cache")
	if err != nil {
		t.Fatal(err)
	}
	if options.TLSConfig == nil || options.TLSConfig.InsecureSkipVerify {
		t.Fatalf("TLS config=%#v", options.TLSConfig)
	}
	if options.TLSConfig.ServerName != "redis.internal" {
		t.Fatalf("server name=%q", options.TLSConfig.ServerName)
	}
	if _, err := certificate.Verify(x509.VerifyOptions{Roots: options.TLSConfig.RootCAs}); err != nil {
		t.Fatalf("private CA was not loaded: %v", err)
	}
}

func TestParseClusterURLLoadsPrivateRootCA(t *testing.T) {
	caFile, certificate := writeTestCA(t)
	options, err := ParseClusterURL("rediss://redis.internal:6379", caFile, "principal-cache")
	if err != nil {
		t.Fatal(err)
	}
	if options.TLSConfig == nil || options.TLSConfig.InsecureSkipVerify {
		t.Fatalf("TLS config=%#v", options.TLSConfig)
	}
	if _, err := certificate.Verify(x509.VerifyOptions{Roots: options.TLSConfig.RootCAs}); err != nil {
		t.Fatalf("private CA was not loaded: %v", err)
	}
}

func TestParseURLRejectsCAOnPlaintextAndInvalidPEM(t *testing.T) {
	caFile, _ := writeTestCA(t)
	if _, err := ParseURL("redis://redis.internal:6379", caFile, "rate-limit"); err == nil || !strings.Contains(err.Error(), "requires a rediss:// URL") {
		t.Fatalf("plaintext error=%v", err)
	}
	invalid := filepath.Join(t.TempDir(), "invalid-ca.pem")
	if err := os.WriteFile(invalid, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseURL("rediss://redis.internal:6379", invalid, "rate-limit"); err == nil || !strings.Contains(err.Error(), "no valid PEM") {
		t.Fatalf("invalid PEM error=%v", err)
	}
}

func TestParseSentinelURLPreservesTLSIdentityAndSeparateCredentials(t *testing.T) {
	caFile, certificate := writeTestCA(t)
	query := url.Values{
		"master_name": {"mymaster"}, "sentinel_addr": {"a.internal:26379", "[fd00::2]:26379"},
		"sentinel_username": {"observer"}, "sentinel_password": {"sentinel:@/%& pass"},
	}
	address := "rediss+sentinel://app:data%40secret@redis.internal:6379/2?" + query.Encode()
	options, err := parseSentinelURL(address, caFile, "rate-limit", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if options.MasterName != "mymaster" || len(options.SentinelAddrs) != 2 || options.SentinelAddrs[1] != "[fd00::2]:26379" || options.DB != 2 {
		t.Fatal("unexpected Sentinel topology")
	}
	if options.Username != "app" || options.Password != "data@secret" ||
		options.SentinelUsername != "observer" || options.SentinelPassword != "sentinel:@/%& pass" {
		t.Fatal("Redis and Sentinel credentials were not parsed independently")
	}
	if options.TLSConfig == nil || options.TLSConfig.InsecureSkipVerify || options.TLSConfig.ServerName != "redis.internal" {
		t.Fatal("the Redis authority must be verified for both data and Sentinel connections")
	}
	if _, err := certificate.Verify(x509.VerifyOptions{Roots: options.TLSConfig.RootCAs}); err != nil {
		t.Fatalf("Sentinel topology did not load the private CA: %v", err)
	}
	if options.DialTimeout != 3*time.Second || options.Dialer == nil || options.ReadTimeout != time.Second || options.WriteTimeout != time.Second ||
		!options.ContextTimeoutEnabled || options.MaxRetries != -1 {
		t.Fatal("Sentinel topology needs bounded I/O and must not replay a timed-out write")
	}
}

func TestParseSentinelURLRejectsMalformedConfigurationWithoutLeakingSecrets(t *testing.T) {
	const basic = "rediss+sentinel://app:data-secret@redis.internal/0?"
	for _, query := range []string{
		"master_name=m&sentinel_password=sentinel-secret",
		"sentinel_addr=one.internal:26379&sentinel_password=sentinel-secret",
		"master_name=m&sentinel_addr=one.internal:26379&sentinel_addr=ONE.internal:26379",
		"master_name=m&sentinel_addr=one.internal:0",
		"master_name=m&sentinel_addr=one.internal:26379&skip_verify=true",
		"master_name=m&sentinel_addr=one.internal:26379&sentinel_username=observer",
		"master_name=m&master_name=n&sentinel_addr=one.internal:26379",
		"master_name=m&sentinel_addr=one.internal:26379&sentinel_password=sentinel-secret%a",
	} {
		_, err := parseSentinelURL(basic+query, "", "principal-cache", time.Second)
		if err == nil || strings.Contains(err.Error(), "data-secret") || strings.Contains(err.Error(), "sentinel-secret") {
			t.Fatal("invalid Sentinel configuration must fail with a redacted error")
		}
	}
}

func writeTestCA(t *testing.T) (string, *x509.Certificate) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Domainry Redis test CA"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "ca.crt")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, certificate
}
