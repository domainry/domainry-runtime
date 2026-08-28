package runtimeext

import (
	"fmt"
	"regexp"
	"strings"
)

var durableIntentContractHashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// DurableIntent is the single transactionally persisted asynchronous message
// produced by an Action. Internal automation, email and other Connector work
// share one storage and delivery lifecycle while retaining consumer-specific
// workers, concurrency, rate limits and adapters.
//
// Generated capabilities provide the consumer, operation and payload contract
// identities. Runtime validates them against the published intent catalog
// before adding the message to the current unit of work.
type DurableIntent struct {
	// IntentID is assigned by Runtime when the intent joins the current UoW.
	// Generated project code must not provide or derive this identity.
	IntentID       string
	ConsumerKey    string
	ConnectionKey  string
	OperationKey   string
	ContractSHA256 string
	ObjectKey      string
	RecordID       string
	IdempotencyKey string
	Payload        map[string]any
}

// DurableIntentReceipt is the stable local identity returned before commit.
// It becomes observable only if the enclosing Action transaction commits.
type DurableIntentReceipt struct {
	ID string
}

func (i DurableIntent) Valid() bool {
	return strings.TrimSpace(i.ConsumerKey) != "" &&
		strings.TrimSpace(i.ConnectionKey) != "" &&
		strings.TrimSpace(i.OperationKey) != "" &&
		durableIntentContractHashPattern.MatchString(i.ContractSHA256)
}

// DurableIntentBatchEntryKey returns a collision-free business idempotency
// identity when an asynchronous operation declares both batch_key and
// entry_key. Runtime combines this identity with workspace, Connector,
// connection and operation in the persistent outbox uniqueness constraint.
func DurableIntentBatchEntryKey(payload map[string]any) string {
	batch := strings.TrimSpace(fmt.Sprint(payload["batch_key"]))
	entry := strings.TrimSpace(fmt.Sprint(payload["entry_key"]))
	if batch == "" || batch == "<nil>" || entry == "" || entry == "<nil>" {
		return ""
	}
	return fmt.Sprintf("batch:%d:%s:entry:%d:%s", len(batch), batch, len(entry), entry)
}
