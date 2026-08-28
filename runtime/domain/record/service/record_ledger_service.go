package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

type RecordLedgerError struct {
	Code     string
	RecordID string
	Sequence int64
	Field    string
}

func (e *RecordLedgerError) Error() string {
	return fmt.Sprintf("%s: record=%s sequence=%d field=%s", e.Code, e.RecordID, e.Sequence, e.Field)
}

func (e *RecordLedgerError) ErrorCode() string { return e.Code }

func (e *RecordLedgerError) ErrorParams() map[string]string {
	return map[string]string{"record_id": e.RecordID, "sequence": strconv.FormatInt(e.Sequence, 10), "field": e.Field}
}

func RecordLedgerEntryFromRecord(object definitionmodel.ObjectSchema, record recordmodel.Record) (recordmodel.RecordLedgerEntry, error) {
	if object.LedgerPolicy == nil || object.LifecyclePolicy == nil || strings.TrimSpace(object.LifecyclePolicy.Mode) != definitionmodel.ObjectLifecycleAppendOnly {
		return recordmodel.RecordLedgerEntry{}, recordLedgerError("backend.ledger.policy_required", record.ID, 0, "ledger_policy")
	}
	entry := recordmodel.RecordLedgerEntry{
		ID: record.ID, AccountID: recordLedgerText(record.Data["account_id"]), BusinessKey: recordLedgerText(record.Data["business_key"]),
		EntryKind: recordLedgerText(record.Data["entry_kind"]), Direction: recordLedgerText(record.Data["direction"]), BalanceBucket: recordLedgerText(record.Data["balance_bucket"]),
		Currency: strings.ToUpper(recordLedgerText(record.Data["currency"])), SourceReference: recordLedgerText(record.Data["source_reference"]), ActorID: recordLedgerText(record.Data["actor_id"]),
		RuleVersion: recordLedgerText(record.Data["rule_version"]), ReversalOf: recordLedgerText(record.Data["reversal_of"]), PreviousHash: recordLedgerText(record.Data["previous_hash"]),
		EntryHash: recordLedgerText(record.Data["entry_hash"]), Signature: recordLedgerText(record.Data["signature"]),
	}
	entry.Sequence, _ = recordLedgerInt64(record.Data["sequence"])
	entry.OccurredAt, _ = time.Parse(time.RFC3339Nano, recordLedgerText(record.Data["occurred_at"]))
	amountField := recordLedgerObjectField(object, "amount")
	config, configErr := recordmodel.RecordNormalizeDecimalConfig(amountField.Config)
	if configErr == nil {
		entry.Amount, configErr = recordmodel.RecordNormalizeDecimal(record.Data["amount"], config)
	}
	if configErr != nil {
		return recordmodel.RecordLedgerEntry{}, recordLedgerError("backend.ledger.amount_invalid", record.ID, entry.Sequence, "amount")
	}
	for field, value := range map[string]string{
		"account_id": entry.AccountID, "business_key": entry.BusinessKey, "entry_kind": entry.EntryKind, "balance_bucket": entry.BalanceBucket,
		"currency": entry.Currency, "source_reference": entry.SourceReference, "actor_id": entry.ActorID, "rule_version": entry.RuleVersion,
	} {
		if value == "" {
			return recordmodel.RecordLedgerEntry{}, recordLedgerError("backend.ledger.field_required", record.ID, entry.Sequence, field)
		}
	}
	if entry.Direction != recordmodel.RecordLedgerDirectionIncrease && entry.Direction != recordmodel.RecordLedgerDirectionDecrease {
		return recordmodel.RecordLedgerEntry{}, recordLedgerError("backend.ledger.direction_invalid", record.ID, entry.Sequence, "direction")
	}
	if entry.Sequence < 1 {
		return recordmodel.RecordLedgerEntry{}, recordLedgerError("backend.ledger.sequence_invalid", record.ID, entry.Sequence, "sequence")
	}
	if entry.OccurredAt.IsZero() {
		return recordmodel.RecordLedgerEntry{}, recordLedgerError("backend.ledger.occurred_at_invalid", record.ID, entry.Sequence, "occurred_at")
	}
	if entry.ReversalOf != "" && entry.EntryKind != recordmodel.RecordLedgerKindReversal && entry.EntryKind != recordmodel.RecordLedgerKindAdjustment {
		return recordmodel.RecordLedgerEntry{}, recordLedgerError("backend.ledger.reversal_kind_invalid", record.ID, entry.Sequence, "entry_kind")
	}
	return entry, nil
}

func RecordLedgerBuildEvidence(object definitionmodel.ObjectSchema, record recordmodel.Record, previousHash string, secret []byte) (recordmodel.Record, error) {
	data := make(map[string]any, len(record.Data)+3)
	for key, value := range record.Data {
		data[key] = value
	}
	record.Data = data
	record.Data["previous_hash"] = strings.TrimSpace(previousHash)
	record.Data["entry_hash"] = "pending"
	if strings.TrimSpace(object.LedgerPolicy.Signature) == definitionmodel.ObjectLedgerSignatureHMACSHA256 {
		record.Data["signature"] = "pending"
	}
	entry, err := RecordLedgerEntryFromRecord(object, record)
	if err != nil {
		return recordmodel.Record{}, err
	}
	entry.PreviousHash = strings.TrimSpace(previousHash)
	entry.EntryHash = recordLedgerHash(entry)
	record.Data["previous_hash"] = entry.PreviousHash
	record.Data["entry_hash"] = entry.EntryHash
	if strings.TrimSpace(object.LedgerPolicy.Signature) == definitionmodel.ObjectLedgerSignatureHMACSHA256 {
		if len(secret) == 0 {
			return recordmodel.Record{}, recordLedgerError("backend.ledger.signature_key_required", record.ID, entry.Sequence, "signature")
		}
		record.Data["signature"] = recordLedgerSignature(entry.EntryHash, secret)
	}
	return record, nil
}

func RecordLedgerReplay(object definitionmodel.ObjectSchema, records []recordmodel.Record, asOf *time.Time, secret []byte) (recordmodel.RecordLedgerReplayResult, error) {
	config, err := recordmodel.RecordNormalizeDecimalConfig(recordLedgerObjectField(object, "amount").Config)
	if err != nil {
		return recordmodel.RecordLedgerReplayResult{}, recordLedgerError("backend.ledger.amount_invalid", "", 0, "amount")
	}
	balances := map[recordmodel.RecordLedgerBalanceKey]string{}
	previousHash := ""
	var previousTime time.Time
	var expectedSequence int64 = 1
	result := recordmodel.RecordLedgerReplayResult{}
	for _, record := range records {
		entry, parseErr := RecordLedgerEntryFromRecord(object, record)
		if parseErr != nil {
			return recordmodel.RecordLedgerReplayResult{}, parseErr
		}
		if asOf != nil && entry.OccurredAt.After(*asOf) {
			break
		}
		if entry.Sequence != expectedSequence {
			return recordmodel.RecordLedgerReplayResult{}, recordLedgerError("backend.ledger.sequence_gap", entry.ID, entry.Sequence, "sequence")
		}
		if !previousTime.IsZero() && entry.OccurredAt.Before(previousTime) {
			return recordmodel.RecordLedgerReplayResult{}, recordLedgerError("backend.ledger.order_invalid", entry.ID, entry.Sequence, "occurred_at")
		}
		if entry.PreviousHash != previousHash {
			return recordmodel.RecordLedgerReplayResult{}, recordLedgerError("backend.ledger.chain_broken", entry.ID, entry.Sequence, "previous_hash")
		}
		if entry.EntryHash != recordLedgerHash(entry) {
			return recordmodel.RecordLedgerReplayResult{}, recordLedgerError("backend.ledger.integrity_mismatch", entry.ID, entry.Sequence, "entry_hash")
		}
		if strings.TrimSpace(object.LedgerPolicy.Signature) == definitionmodel.ObjectLedgerSignatureHMACSHA256 {
			if len(secret) == 0 || !hmac.Equal([]byte(entry.Signature), []byte(recordLedgerSignature(entry.EntryHash, secret))) {
				return recordmodel.RecordLedgerReplayResult{}, recordLedgerError("backend.ledger.signature_mismatch", entry.ID, entry.Sequence, "signature")
			}
		}
		key := recordmodel.RecordLedgerBalanceKey{AccountID: entry.AccountID, Currency: entry.Currency, Bucket: entry.BalanceBucket}
		current := balances[key]
		if current == "" {
			current, _ = recordmodel.RecordNormalizeDecimal("0", config)
		}
		if entry.Direction == recordmodel.RecordLedgerDirectionIncrease {
			balances[key], err = recordmodel.RecordAddDecimals(current, entry.Amount, config)
		} else {
			balances[key], err = recordmodel.RecordSubtractDecimals(current, entry.Amount, config)
		}
		if err != nil {
			return recordmodel.RecordLedgerReplayResult{}, recordLedgerError("backend.ledger.balance_invalid", entry.ID, entry.Sequence, "amount")
		}
		previousHash, previousTime, expectedSequence = entry.EntryHash, entry.OccurredAt, entry.Sequence+1
		result.Entries++
	}
	result.LastHash = previousHash
	for key, amount := range balances {
		result.Balances = append(result.Balances, recordmodel.RecordLedgerBalance{Key: key, Amount: amount})
	}
	sort.Slice(result.Balances, func(i, j int) bool {
		left, right := result.Balances[i].Key, result.Balances[j].Key
		if left.AccountID != right.AccountID {
			return left.AccountID < right.AccountID
		}
		if left.Currency != right.Currency {
			return left.Currency < right.Currency
		}
		return left.Bucket < right.Bucket
	})
	return result, nil
}

func recordLedgerHash(entry recordmodel.RecordLedgerEntry) string {
	payload, _ := json.Marshal(struct {
		ID, AccountID, BusinessKey, EntryKind, Direction, BalanceBucket, Amount, Currency, SourceReference, ActorID, RuleVersion, OccurredAt, ReversalOf, PreviousHash string
		Sequence                                                                                                                                                       int64
	}{entry.ID, entry.AccountID, entry.BusinessKey, entry.EntryKind, entry.Direction, entry.BalanceBucket, entry.Amount, entry.Currency, entry.SourceReference, entry.ActorID, entry.RuleVersion, entry.OccurredAt.UTC().Format(time.RFC3339Nano), entry.ReversalOf, entry.PreviousHash, entry.Sequence})
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func recordLedgerSignature(hash string, secret []byte) string {
	digest := hmac.New(sha256.New, secret)
	_, _ = digest.Write([]byte(hash))
	return hex.EncodeToString(digest.Sum(nil))
}

func recordLedgerObjectField(object definitionmodel.ObjectSchema, key string) definitionmodel.FieldSchema {
	for _, field := range object.Fields {
		if field.Key == key {
			return field
		}
	}
	return definitionmodel.FieldSchema{}
}

func recordLedgerText(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func recordLedgerInt64(value any) (int64, bool) {
	switch typed := value.(type) {
	case int:
		return int64(typed), true
	case int64:
		return typed, true
	case float64:
		if typed == float64(int64(typed)) {
			return int64(typed), true
		}
	case json.Number:
		parsed, err := typed.Int64()
		return parsed, err == nil
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return parsed, err == nil
	}
	return 0, false
}

func recordLedgerError(code, recordID string, sequence int64, field string) error {
	return &RecordLedgerError{Code: code, RecordID: recordID, Sequence: sequence, Field: field}
}
