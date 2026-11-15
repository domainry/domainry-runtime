package redisconnect

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
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
