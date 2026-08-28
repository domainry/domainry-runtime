package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"hash/fnv"
	"strings"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

const IntegrationEventMaxAttempts = 5
const IntegrationOutboxMaxAttempts = 5

func IntegrationRetryDelaySeconds(attemptCount int) int {
	switch {
	case attemptCount <= 1:
		return 60
	case attemptCount == 2:
		return 300
	case attemptCount == 3:
		return 900
	default:
		return 3600
	}
}

// IntegrationRetryDelaySecondsFor adds deterministic per-task jitter so a
// provider recovery does not release an entire retry cohort simultaneously.
func IntegrationRetryDelaySecondsFor(taskID string, attemptCount int) int {
	base := IntegrationRetryDelaySeconds(attemptCount)
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(strings.TrimSpace(taskID)))
	spread := base / 5
	offset := int(hash.Sum32()%uint32(spread*2+1)) - spread
	delay := base + offset
	if delay > 3600 {
		return 3600
	}
	return delay
}

func IntegrationWorkerPrincipal(workspaceID string) principalmodel.Principal {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return principalmodel.Principal{}
	}
	principal := principalmodel.NewSystemPrincipal(
		"integration:worker",
		principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "integration worker dispatch"),
		"*",
	)
	principal.WorkspaceID = workspaceID
	return principal
}

func IntegrationSanitizeKey(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "integration"
	}
	var builder strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			builder.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			builder.WriteRune(r + ('a' - 'A'))
		case r >= '0' && r <= '9':
			builder.WriteRune(r)
		default:
			builder.WriteByte('_')
		}
	}
	out := strings.Trim(builder.String(), "_")
	if out == "" {
		return "integration"
	}
	return out
}

func IntegrationShortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:16]
}
