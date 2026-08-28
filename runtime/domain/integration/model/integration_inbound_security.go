package integrationmodel

import "time"

const IntegrationInboundSecurityProfileDevice = "device"

type IntegrationInboundSecurityPolicy struct {
	Profile        string
	MaxSkewSeconds int64
}

type IntegrationInboundSecurityEvidence struct {
	SignatureVerified bool
	Nonce             string
	DeviceIdentity    string
	EventTime         string
	ExternalID        string
	Now               time.Time
}

type IntegrationInboundSecurityValidation struct {
	Valid     bool
	Failure   string
	EventTime time.Time
}
