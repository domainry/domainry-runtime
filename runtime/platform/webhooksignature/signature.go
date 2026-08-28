package webhooksignature

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"
)

type Error string

const (
	ErrorInvalidAlgorithm    Error = "invalid_algorithm"
	ErrorInvalidTimestamp    Error = "invalid_timestamp"
	ErrorTimestampOutOfRange Error = "timestamp_out_of_range"
	ErrorMissingNonce        Error = "missing_nonce"
	ErrorInvalidSignature    Error = "invalid_signature"
)

func (e Error) Error() string { return string(e) }

func IsError(err error, target Error) bool { return errors.Is(err, target) }

type Verification struct {
	Algorithm      string
	Secret         string
	Timestamp      string
	Nonce          string
	Signature      string
	Body           []byte
	MaxSkewSeconds int64
	Now            time.Time
}

func NormalizeAlgorithm(value string) string {
	switch strings.TrimSpace(value) {
	case "feishu_sha256_hex", "hmac_sha256_hex":
		return strings.TrimSpace(value)
	default:
		return ""
	}
}

func Compute(algorithm, secret, timestamp, nonce string, body []byte) string {
	switch NormalizeAlgorithm(algorithm) {
	case "feishu_sha256_hex":
		sum := sha256.Sum256(append([]byte(timestamp+nonce+secret), body...))
		return hex.EncodeToString(sum[:])
	case "hmac_sha256_hex":
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write([]byte(timestamp))
		_, _ = mac.Write([]byte(nonce))
		_, _ = mac.Write(body)
		return hex.EncodeToString(mac.Sum(nil))
	default:
		return ""
	}
}

func Verify(req Verification) error {
	algorithm := NormalizeAlgorithm(req.Algorithm)
	if algorithm == "" {
		return ErrorInvalidAlgorithm
	}
	timestamp, nonce := strings.TrimSpace(req.Timestamp), strings.TrimSpace(req.Nonce)
	if req.MaxSkewSeconds > 0 {
		parsed, err := strconv.ParseInt(timestamp, 10, 64)
		if err != nil || parsed <= 0 {
			return ErrorInvalidTimestamp
		}
		now := req.Now
		if now.IsZero() {
			now = time.Now().UTC()
		}
		if parsed > now.Unix()+req.MaxSkewSeconds || parsed < now.Unix()-req.MaxSkewSeconds {
			return ErrorTimestampOutOfRange
		}
		if nonce == "" {
			return ErrorMissingNonce
		}
	}
	expected := Compute(algorithm, req.Secret, timestamp, nonce, req.Body)
	actual := strings.TrimSpace(req.Signature)
	if actual == "" || !hmac.Equal([]byte(strings.ToLower(expected)), []byte(strings.ToLower(actual))) {
		return ErrorInvalidSignature
	}
	return nil
}
