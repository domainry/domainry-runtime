package integrationmodel

import "time"

type IntegrationWebhookSignatureCheck struct {
	Algorithm      string
	Secret         string
	Timestamp      string
	Nonce          string
	Signature      string
	Body           []byte
	MaxSkewSeconds int64
	Now            time.Time
}

type IntegrationWebhookSignatureCheckResult struct {
	Algorithm string
	Failure   string
}
