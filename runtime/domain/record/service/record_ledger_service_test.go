package service

import (
	"errors"
	"testing"
	"time"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestRecordLedgerEvidenceReplayAndTamperDetection(t *testing.T) {
	object := recordLedgerTestObject()
	secret := []byte("workspace-ledger-signing-key")
	inputs := []map[string]any{
		{"account_id": "account-1", "business_key": "payment-1", "entry_kind": "payment", "direction": "increase", "balance_bucket": "principal", "amount": "100.00", "currency": "CNY", "source_reference": "payment:1", "actor_id": "cashier-1", "rule_version": "1", "occurred_at": "2026-07-21T09:00:00Z", "sequence": 1, "reversal_of": ""},
		{"account_id": "account-1", "business_key": "bonus-1", "entry_kind": "grant", "direction": "increase", "balance_bucket": "bonus", "amount": "20.00", "currency": "CNY", "source_reference": "campaign:1", "actor_id": "system", "rule_version": "2", "occurred_at": "2026-07-21T09:01:00Z", "sequence": 2, "reversal_of": ""},
		{"account_id": "account-1", "business_key": "refund-1", "entry_kind": "reversal", "direction": "decrease", "balance_bucket": "principal", "amount": "30.00", "currency": "CNY", "source_reference": "refund:1", "actor_id": "finance-1", "rule_version": "3", "occurred_at": "2026-07-21T09:02:00Z", "sequence": 3, "reversal_of": "entry-1"},
	}
	records := make([]recordmodel.Record, 0, len(inputs))
	previous := ""
	for index, data := range inputs {
		record, err := RecordLedgerBuildEvidence(object, recordmodel.Record{ID: "entry-" + string(rune('1'+index)), Data: data}, previous, secret)
		if err != nil {
			t.Fatal(err)
		}
		previous = record.Data["entry_hash"].(string)
		records = append(records, record)
	}
	result, err := RecordLedgerReplay(object, records, nil, secret)
	if err != nil || result.Entries != 3 || result.LastHash != previous || len(result.Balances) != 2 || result.Balances[0].Key.Bucket != "bonus" || result.Balances[0].Amount != "20.00" || result.Balances[1].Key.Bucket != "principal" || result.Balances[1].Amount != "70.00" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	asOf := time.Date(2026, 7, 21, 9, 1, 30, 0, time.UTC)
	partial, err := RecordLedgerReplay(object, records, &asOf, secret)
	if err != nil || partial.Entries != 2 || partial.Balances[1].Amount != "100.00" {
		t.Fatalf("partial=%#v err=%v", partial, err)
	}

	tampered := cloneLedgerRecords(records)
	tampered[1].Data["amount"] = "21.00"
	assertRecordLedgerError(t, RecordLedgerReplayError(object, tampered, secret), "backend.ledger.integrity_mismatch", "entry-2", 2)
	gapped := cloneLedgerRecords(records)
	gapped[1].Data["sequence"] = 4
	assertRecordLedgerError(t, RecordLedgerReplayError(object, gapped, secret), "backend.ledger.sequence_gap", "entry-2", 4)
	outOfOrder := cloneLedgerRecords(records)
	outOfOrder[1].Data["occurred_at"] = "2026-07-21T08:59:00Z"
	assertRecordLedgerError(t, RecordLedgerReplayError(object, outOfOrder, secret), "backend.ledger.order_invalid", "entry-2", 2)
	badSignature := cloneLedgerRecords(records)
	badSignature[1].Data["signature"] = "tampered"
	assertRecordLedgerError(t, RecordLedgerReplayError(object, badSignature, secret), "backend.ledger.signature_mismatch", "entry-2", 2)
}

func TestRecordLedgerRequiresAdjustmentKindForReversalReference(t *testing.T) {
	object := recordLedgerTestObject()
	record := recordmodel.Record{ID: "entry-1", Data: map[string]any{"account_id": "a", "business_key": "b", "entry_kind": "payment", "direction": "decrease", "balance_bucket": "cash", "amount": "1.00", "currency": "CNY", "source_reference": "s", "actor_id": "u", "rule_version": "1", "occurred_at": "2026-07-21T00:00:00Z", "sequence": 1, "reversal_of": "original"}}
	_, err := RecordLedgerBuildEvidence(object, record, "", []byte("secret"))
	assertRecordLedgerError(t, err, "backend.ledger.reversal_kind_invalid", "entry-1", 1)
}

func RecordLedgerReplayError(object definitionmodel.ObjectSchema, records []recordmodel.Record, secret []byte) error {
	_, err := RecordLedgerReplay(object, records, nil, secret)
	return err
}

func assertRecordLedgerError(t *testing.T, err error, code, recordID string, sequence int64) {
	t.Helper()
	var typed *RecordLedgerError
	if !errors.As(err, &typed) || typed.Code != code || typed.RecordID != recordID || typed.Sequence != sequence {
		t.Fatalf("error=%#v want code=%s record=%s sequence=%d", err, code, recordID, sequence)
	}
}

func cloneLedgerRecords(records []recordmodel.Record) []recordmodel.Record {
	cloned := make([]recordmodel.Record, len(records))
	for index, record := range records {
		data := map[string]any{}
		for key, value := range record.Data {
			data[key] = value
		}
		cloned[index] = recordmodel.Record{ID: record.ID, Data: data}
	}
	return cloned
}

func recordLedgerTestObject() definitionmodel.ObjectSchema {
	fields := []definitionmodel.FieldSchema{}
	for _, key := range []string{"account_id", "business_key", "entry_kind", "direction", "balance_bucket", "currency", "source_reference", "actor_id", "rule_version", "reversal_of", "previous_hash", "entry_hash", "signature"} {
		fields = append(fields, definitionmodel.FieldSchema{Key: key, Type: "text"})
	}
	fields = append(fields, definitionmodel.FieldSchema{Key: "amount", Type: "currency", Config: map[string]any{"precision": 19, "scale": 2}}, definitionmodel.FieldSchema{Key: "occurred_at", Type: "datetime"}, definitionmodel.FieldSchema{Key: "sequence", Type: "number"})
	return definitionmodel.ObjectSchema{Key: "financial_entry", Fields: fields, LifecyclePolicy: &definitionmodel.ObjectLifecyclePolicy{Mode: definitionmodel.ObjectLifecycleAppendOnly}, LedgerPolicy: &definitionmodel.ObjectLedgerPolicy{Integrity: definitionmodel.ObjectLedgerIntegritySHA256Chain, Signature: definitionmodel.ObjectLedgerSignatureHMACSHA256}}
}
