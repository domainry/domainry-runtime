package auditmodel

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

var auditEventSequence atomic.Uint64

func NewEventID(now time.Time) string {
	return fmt.Sprintf("audit_%d_%d", now.UnixNano(), auditEventSequence.Add(1))
}

func IdempotentEventID(workspaceID, key string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(workspaceID) + "\x00" + strings.TrimSpace(key)))
	return "audit_idem_" + hex.EncodeToString(digest[:16])
}
