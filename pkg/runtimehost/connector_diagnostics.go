package runtimehost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/telemetry"
)

func recordConnectorDependency(ctx context.Context, feature, dependency, operation string, input, output map[string]any, started time.Time, err error) {
	telemetry.RecordDependencyCall(ctx, telemetry.DependencyCall{
		Feature: feature, Dependency: dependency, Operation: operation, Input: input,
	}, telemetry.DependencyResult{Output: output, Err: err}, time.Since(started))
}

func connectorDiagnosticHash(value string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return hex.EncodeToString(digest[:8])
}
