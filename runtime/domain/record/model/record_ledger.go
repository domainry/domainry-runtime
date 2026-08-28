package recordmodel

import "time"

const (
	RecordLedgerDirectionIncrease = "increase"
	RecordLedgerDirectionDecrease = "decrease"
	RecordLedgerKindReversal      = "reversal"
	RecordLedgerKindAdjustment    = "adjustment"
)

// RecordLedgerEntry is the canonical business-neutral view of a ledger record.
// Domain-specific classifications belong in BusinessKey, EntryKind and
// BalanceBucket; Runtime owns only ordering, exact amount and evidence fields.
type RecordLedgerEntry struct {
	ID              string
	AccountID       string
	BusinessKey     string
	EntryKind       string
	Direction       string
	BalanceBucket   string
	Amount          string
	Currency        string
	SourceReference string
	ActorID         string
	RuleVersion     string
	OccurredAt      time.Time
	Sequence        int64
	ReversalOf      string
	PreviousHash    string
	EntryHash       string
	Signature       string
}

type RecordLedgerBalanceKey struct {
	AccountID string `json:"account_id"`
	Currency  string `json:"currency"`
	Bucket    string `json:"bucket"`
}

type RecordLedgerBalance struct {
	Key    RecordLedgerBalanceKey `json:"key"`
	Amount string                 `json:"amount"`
}

type RecordLedgerReplayResult struct {
	Entries  int                   `json:"entries"`
	LastHash string                `json:"last_hash"`
	Balances []RecordLedgerBalance `json:"balances"`
}
