package webhooksignature

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestNormalizeAlgorithmAndComputeKnownVectors(t *testing.T) {
	body := []byte(`{"ok":true}`)
	tests := []struct {
		name      string
		algorithm string
		want      string
	}{
		{name: "feishu", algorithm: " feishu_sha256_hex ", want: "06db0dfc1df6a60b5b2485e1d3596f5296dce8f36af2acde8eb91f1023088cdc"},
		{name: "hmac", algorithm: "hmac_sha256_hex", want: "52f0ccccd1c897ca03df1aab748f9c00519740fb2c2eb803d422ae9115f17b03"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := Compute(test.algorithm, "secret", "1700000000", "nonce-1", body); got != test.want {
				t.Fatalf("Compute() = %q, want %q", got, test.want)
			}
		})
	}
	for _, value := range []string{"", "sha256", "HMAC_SHA256_HEX"} {
		if got := NormalizeAlgorithm(value); got != "" {
			t.Fatalf("NormalizeAlgorithm(%q) = %q, want empty", value, got)
		}
		if got := Compute(value, "secret", "1700000000", "nonce-1", body); got != "" {
			t.Fatalf("Compute(%q) = %q, want empty", value, got)
		}
	}
}

func TestVerifyRejectsMalformedAndExpiredRequests(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	base := Verification{
		Algorithm:      "hmac_sha256_hex",
		Secret:         "secret",
		Timestamp:      "1700000000",
		Nonce:          "nonce-1",
		Body:           []byte(`{"ok":true}`),
		MaxSkewSeconds: 60,
		Now:            now,
	}
	base.Signature = Compute(base.Algorithm, base.Secret, base.Timestamp, base.Nonce, base.Body)

	tests := []struct {
		name   string
		mutate func(*Verification)
		want   Error
	}{
		{name: "algorithm", mutate: func(req *Verification) { req.Algorithm = "md5" }, want: ErrorInvalidAlgorithm},
		{name: "timestamp text", mutate: func(req *Verification) { req.Timestamp = "yesterday" }, want: ErrorInvalidTimestamp},
		{name: "timestamp zero", mutate: func(req *Verification) { req.Timestamp = "0" }, want: ErrorInvalidTimestamp},
		{name: "timestamp future", mutate: func(req *Verification) { req.Timestamp = "1700000061" }, want: ErrorTimestampOutOfRange},
		{name: "timestamp past", mutate: func(req *Verification) { req.Timestamp = "1699999939" }, want: ErrorTimestampOutOfRange},
		{name: "nonce", mutate: func(req *Verification) { req.Nonce = " " }, want: ErrorMissingNonce},
		{name: "signature empty", mutate: func(req *Verification) { req.Signature = " " }, want: ErrorInvalidSignature},
		{name: "signature mismatch", mutate: func(req *Verification) { req.Signature = strings.Repeat("0", 64) }, want: ErrorInvalidSignature},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := base
			test.mutate(&req)
			err := Verify(req)
			if !IsError(err, test.want) {
				t.Fatalf("Verify() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestVerifyAcceptsBoundariesCaseInsensitiveSignatureAndOptionalReplayFields(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	for _, timestamp := range []string{"1699999940", "1700000060"} {
		req := Verification{Algorithm: "feishu_sha256_hex", Secret: "secret", Timestamp: timestamp, Nonce: "nonce-1", Body: []byte("payload"), MaxSkewSeconds: 60, Now: now}
		req.Signature = strings.ToUpper(Compute(req.Algorithm, req.Secret, req.Timestamp, req.Nonce, req.Body))
		if err := Verify(req); err != nil {
			t.Fatalf("Verify() rejected timestamp boundary %s: %v", timestamp, err)
		}
	}

	withoutReplayWindow := Verification{Algorithm: "hmac_sha256_hex", Secret: "secret", Body: []byte("payload")}
	withoutReplayWindow.Signature = Compute(withoutReplayWindow.Algorithm, withoutReplayWindow.Secret, "", "", withoutReplayWindow.Body)
	if err := Verify(withoutReplayWindow); err != nil {
		t.Fatalf("Verify() rejected request with disabled replay window: %v", err)
	}

	usingClock := Verification{Algorithm: "hmac_sha256_hex", Secret: "secret", Timestamp: strconv.FormatInt(time.Now().UTC().Unix(), 10), Nonce: "nonce-1", MaxSkewSeconds: 5}
	usingClock.Signature = Compute(usingClock.Algorithm, usingClock.Secret, usingClock.Timestamp, usingClock.Nonce, nil)
	if err := Verify(usingClock); err != nil {
		t.Fatalf("Verify() rejected request using the system clock: %v", err)
	}
}

func TestWebhookSignatureErrorSupportsErrorsIs(t *testing.T) {
	if got := ErrorInvalidSignature.Error(); got != "invalid_signature" {
		t.Fatalf("Error() = %q", got)
	}
	wrapped := errors.Join(errors.New("verification failed"), ErrorInvalidSignature)
	if !IsError(wrapped, ErrorInvalidSignature) {
		t.Fatal("IsError() did not match wrapped webhook signature error")
	}
	if IsError(wrapped, ErrorMissingNonce) {
		t.Fatal("IsError() matched a different webhook signature error")
	}
}
