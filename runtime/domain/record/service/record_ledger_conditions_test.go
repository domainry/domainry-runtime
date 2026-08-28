package service

import (
	"encoding/json"
	"testing"
	"time"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestRecordLedgerEntryValidationEdges(t *testing.T) {
	validObject := recordLedgerTestObject()
	validData := recordLedgerConditionData()

	objects := []definitionmodel.ObjectSchema{
		{},
		{LedgerPolicy: &definitionmodel.ObjectLedgerPolicy{}},
		{LedgerPolicy: &definitionmodel.ObjectLedgerPolicy{}, LifecyclePolicy: &definitionmodel.ObjectLifecyclePolicy{Mode: "mutable"}},
	}
	for _, object := range objects {
		if _, err := RecordLedgerEntryFromRecord(object, recordmodel.Record{ID: "entry", Data: validData}); err == nil {
			t.Fatal("invalid ledger policy accepted")
		}
	}

	badConfig := validObject
	amount := recordLedgerObjectField(badConfig, "amount")
	amount.Config = map[string]any{"precision": 0}
	for index := range badConfig.Fields {
		if badConfig.Fields[index].Key == "amount" {
			badConfig.Fields[index] = amount
		}
	}
	if _, err := RecordLedgerEntryFromRecord(badConfig, recordmodel.Record{ID: "entry", Data: validData}); err == nil {
		t.Fatal("invalid amount config accepted")
	}
	validObject = recordLedgerTestObject()
	badAmount := cloneLedgerData(validData)
	badAmount["amount"] = struct{}{}
	if _, err := RecordLedgerEntryFromRecord(validObject, recordmodel.Record{ID: "entry", Data: badAmount}); err == nil {
		t.Fatal("invalid amount accepted")
	}

	for _, mutation := range []func(map[string]any){
		func(data map[string]any) { data["account_id"] = " " },
		func(data map[string]any) { data["direction"] = "sideways" },
		func(data map[string]any) { data["sequence"] = 0 },
		func(data map[string]any) { data["occurred_at"] = "bad" },
	} {
		data := cloneLedgerData(validData)
		mutation(data)
		if _, err := RecordLedgerEntryFromRecord(validObject, recordmodel.Record{ID: "entry", Data: data}); err == nil {
			t.Fatal("invalid ledger entry accepted")
		}
	}
	decrease := cloneLedgerData(validData)
	decrease["direction"] = recordmodel.RecordLedgerDirectionDecrease
	if _, err := RecordLedgerEntryFromRecord(validObject, recordmodel.Record{ID: "entry", Data: decrease}); err != nil {
		t.Fatalf("decrease rejected: %v", err)
	}
	adjustment := cloneLedgerData(validData)
	adjustment["entry_kind"] = recordmodel.RecordLedgerKindAdjustment
	adjustment["reversal_of"] = "original"
	if _, err := RecordLedgerEntryFromRecord(validObject, recordmodel.Record{ID: "entry", Data: adjustment}); err != nil {
		t.Fatalf("adjustment rejected: %v", err)
	}
}

func TestRecordLedgerEvidenceAndReplayDependencyEdges(t *testing.T) {
	object := recordLedgerTestObject()
	if _, err := RecordLedgerBuildEvidence(object, recordmodel.Record{ID: "entry", Data: recordLedgerConditionData()}, " ", nil); err == nil {
		t.Fatal("missing signature key accepted")
	}
	unsigned := object
	unsigned.LedgerPolicy = &definitionmodel.ObjectLedgerPolicy{Integrity: definitionmodel.ObjectLedgerIntegritySHA256Chain}
	record, err := RecordLedgerBuildEvidence(unsigned, recordmodel.Record{ID: "entry", Data: recordLedgerConditionData()}, " ", nil)
	if err != nil || record.Data["signature"] != nil {
		t.Fatalf("unsigned record=%#v err=%v", record, err)
	}
	if _, err := RecordLedgerReplay(unsigned, []recordmodel.Record{record}, nil, nil); err != nil {
		t.Fatalf("unsigned replay: %v", err)
	}
	if _, err := RecordLedgerReplay(object, []recordmodel.Record{record}, nil, nil); err == nil {
		t.Fatal("missing replay signature accepted")
	}

	badConfig := recordLedgerTestObject()
	badConfig.LedgerPolicy.Signature = ""
	for index := range badConfig.Fields {
		if badConfig.Fields[index].Key == "amount" {
			badConfig.Fields[index].Config = map[string]any{"precision": 0}
		}
	}
	if _, err := RecordLedgerReplay(badConfig, nil, nil, nil); err == nil {
		t.Fatal("invalid replay decimal config accepted")
	}
	unsigned = recordLedgerTestObject()
	unsigned.LedgerPolicy.Signature = ""
	if _, err := RecordLedgerReplay(unsigned, []recordmodel.Record{{ID: "bad", Data: map[string]any{}}}, nil, nil); err == nil {
		t.Fatal("invalid replay record accepted")
	}
	broken := record
	broken.Data = cloneLedgerData(record.Data)
	broken.Data["previous_hash"] = "wrong"
	if _, err := RecordLedgerReplay(unsigned, []recordmodel.Record{broken}, nil, nil); err == nil {
		t.Fatal("broken chain accepted")
	}
}

func TestRecordLedgerReplayCoversOverflowAndBalanceSortKeys(t *testing.T) {
	object := recordLedgerTestObject()
	for index := range object.Fields {
		if object.Fields[index].Key == "amount" {
			object.Fields[index].Config = map[string]any{"precision": 3, "scale": 2}
		}
	}
	object.LedgerPolicy.Signature = ""
	overflow := []map[string]any{recordLedgerConditionData(), recordLedgerConditionData()}
	overflow[0]["amount"], overflow[1]["amount"] = "9.99", "9.99"
	overflow[1]["sequence"], overflow[1]["occurred_at"] = 2, "2026-07-21T00:01:00Z"
	records := recordLedgerBuildConditionRecords(t, object, overflow)
	if _, err := RecordLedgerReplay(object, records, nil, nil); err == nil {
		t.Fatal("balance overflow accepted")
	}

	object = recordLedgerTestObject()
	object.LedgerPolicy.Signature = ""
	inputs := []map[string]any{recordLedgerConditionData(), recordLedgerConditionData(), recordLedgerConditionData()}
	inputs[0]["account_id"], inputs[0]["currency"], inputs[0]["balance_bucket"] = "b", "USD", "z"
	inputs[1]["account_id"], inputs[1]["currency"], inputs[1]["balance_bucket"] = "a", "USD", "z"
	inputs[2]["account_id"], inputs[2]["currency"], inputs[2]["balance_bucket"] = "a", "CNY", "a"
	for index := range inputs {
		inputs[index]["sequence"] = index + 1
		inputs[index]["occurred_at"] = time.Date(2026, 7, 21, 0, index, 0, 0, time.UTC).Format(time.RFC3339Nano)
	}
	result, err := RecordLedgerReplay(object, recordLedgerBuildConditionRecords(t, object, inputs), nil, nil)
	if err != nil || len(result.Balances) != 3 || result.Balances[0].Key.Currency != "CNY" || result.Balances[2].Key.AccountID != "b" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestRecordLedgerHelpersCoverSupportedAndRejectedInputs(t *testing.T) {
	if recordLedgerText(nil) != "" || recordLedgerText(" value ") != "value" {
		t.Fatal("text normalization mismatch")
	}
	values := []struct {
		value any
		want  int64
		ok    bool
	}{
		{int(2), 2, true}, {int64(3), 3, true}, {float64(4), 4, true}, {float64(4.5), 0, false},
		{json.Number("5"), 5, true}, {json.Number("bad"), 0, false}, {" 6 ", 6, true}, {"bad", 0, false}, {uint(7), 0, false},
	}
	for _, testCase := range values {
		got, ok := recordLedgerInt64(testCase.value)
		if got != testCase.want || ok != testCase.ok {
			t.Fatalf("value=%#v got=(%d,%t)", testCase.value, got, ok)
		}
	}
	if field := recordLedgerObjectField(definitionmodel.ObjectSchema{}, "missing"); field.Key != "" {
		t.Fatalf("field=%#v", field)
	}
	err := &RecordLedgerError{Code: "code", RecordID: "record", Sequence: 7, Field: "amount"}
	if err.ErrorCode() != "code" || err.Error() == "" || err.ErrorParams()["sequence"] != "7" {
		t.Fatalf("ledger error=%#v", err)
	}
}

func recordLedgerConditionData() map[string]any {
	return map[string]any{"account_id": "account", "business_key": "business", "entry_kind": "payment", "direction": "increase", "balance_bucket": "principal", "amount": "1.00", "currency": "cny", "source_reference": "source", "actor_id": "actor", "rule_version": "1", "occurred_at": "2026-07-21T00:00:00Z", "sequence": 1, "reversal_of": ""}
}

func cloneLedgerData(data map[string]any) map[string]any {
	cloned := make(map[string]any, len(data))
	for key, value := range data {
		cloned[key] = value
	}
	return cloned
}

func recordLedgerBuildConditionRecords(t *testing.T, object definitionmodel.ObjectSchema, inputs []map[string]any) []recordmodel.Record {
	t.Helper()
	records := make([]recordmodel.Record, 0, len(inputs))
	previous := ""
	for index, input := range inputs {
		record, err := RecordLedgerBuildEvidence(object, recordmodel.Record{ID: "entry-" + string(rune('1'+index)), Data: input}, previous, nil)
		if err != nil {
			t.Fatal(err)
		}
		previous = record.Data["entry_hash"].(string)
		records = append(records, record)
	}
	return records
}
