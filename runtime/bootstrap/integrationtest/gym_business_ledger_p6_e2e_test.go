package integrationtest

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/shopspring/decimal"

	runtimetestkit "github.com/domainry/domainry-runtime/runtime/bootstrap/testkit"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordservice "github.com/domainry/domainry-runtime/runtime/domain/record/service"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestGymLedgerReplaysExactBalancesAndLocatesTampering(t *testing.T) {
	store, err := persistence.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "gym-ledger-p6.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	object := gymLedgerP6Object()
	planToProduceCreateTable(t, store, object)
	repository := recordpersistence.NewRecordStore(store)
	secret := []byte("gym-fixture-ledger-signing-key")
	entries := []map[string]any{
		gymLedgerP6Data("payment-1", "payment", "increase", "principal", "100.00", "payment:1", 1, "2026-07-21T09:00:00Z", ""),
		gymLedgerP6Data("bonus-1", "grant", "increase", "bonus", "20.00", "campaign:1", 2, "2026-07-21T09:01:00Z", ""),
		gymLedgerP6Data("entry-1-spend", "spend", "decrease", "principal", "30.00", "entry:1", 3, "2026-07-21T09:02:00Z", ""),
		gymLedgerP6Data("refund-1", "reversal", "increase", "refund", "10.00", "refund:1", 4, "2026-07-21T09:03:00Z", "ledger-1"),
		gymLedgerP6Data("cash-in-1", "payment", "increase", "cashflow", "100.00", "payment:1", 5, "2026-07-21T09:04:00Z", ""),
		gymLedgerP6Data("cash-out-1", "reversal", "decrease", "cashflow", "10.00", "refund:1", 6, "2026-07-21T09:05:00Z", "ledger-5"),
	}
	previousHash := ""
	for index, data := range entries {
		now := fmt.Sprintf("2026-07-21T09:%02d:00Z", index)
		record, buildErr := recordservice.RecordLedgerBuildEvidence(object, recordmodel.Record{ID: fmt.Sprintf("ledger-%d", index+1), Data: data, CreatedAt: now, UpdatedAt: now}, previousHash, secret)
		if buildErr != nil {
			t.Fatalf("build entry %d: %v", index+1, buildErr)
		}
		if insertErr := repository.InsertRecord(t.Context(), "default", object, record); insertErr != nil {
			t.Fatalf("insert entry %d: %v", index+1, insertErr)
		}
		previousHash = fmt.Sprint(record.Data["entry_hash"])
	}
	page, err := repository.ListRecords(t.Context(), "default", object, recordmodel.RecordListQuery{Page: 1, PageSize: 20, Sort: []recordmodel.RecordSortRule{{Field: "sequence", Direction: "asc"}}})
	if err != nil {
		t.Fatal(err)
	}
	full, err := recordservice.RecordLedgerReplay(object, page.Items, nil, secret)
	if err != nil || full.Entries != 6 || full.LastHash != previousHash {
		t.Fatalf("full replay=%#v err=%v", full, err)
	}
	gymLedgerP6AssertBalances(t, full, map[string]string{"bonus": "20.00", "cashflow": "90.00", "principal": "70.00", "refund": "10.00"})
	role := accessfixture.Bundle{Key: "finance", Permissions: []string{"gym_financial_ledger.read"}, RecordScope: "all_records", DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: object.Key, Scope: "all_records", Read: true}}}
	field := func(key string) reportmodel.ReportDatasetField {
		return reportmodel.ReportDatasetField{SourceAlias: "ledger", FieldKey: key}
	}
	report := reportmodel.ReportSchema{Key: "gym_ledger_reconciliation", RequiredPermissions: []string{"gym_financial_ledger.read"}, Dataset: reportmodel.ReportDatasetSchema{
		Source:     reportmodel.ReportDatasetSource{ObjectKey: object.Key, Alias: "ledger"},
		Dimensions: []reportmodel.ReportDatasetDimension{{Key: "bucket", Field: field("balance_bucket")}, {Key: "direction", Field: field("direction")}},
		Measures:   []reportmodel.ReportDatasetMeasure{{Key: "entries", Operation: "count", SourceAlias: "ledger"}, {Key: "amount", Operation: "sum", Field: func() *reportmodel.ReportDatasetField { value := field("amount"); return &value }()}},
	}}
	services := runtimetestkit.NewRuntimeServices(t.Context(), runtimetestkit.RuntimeServicesConfig{TemplateID: "gym-ledger-report-p7", TemplateVersion: "1", Name: "Gym Ledger Report P7", Objects: []definitionmodel.ObjectSchema{object}, Reports: []reportmodel.ReportSchema{report}, Integrations: integrationmodel.IntegrationSchema{}, Store: store})
	summary, err := services.Applications().Reports.Summary(t.Context(), report.Key, accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "finance", WorkspaceID: "default"}}, role))
	if err != nil {
		t.Fatal(err)
	}
	reportBalances := map[string]decimal.Decimal{}
	for _, row := range summary.Rows {
		amount, parseErr := decimal.NewFromString(row.Measures["amount"])
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		if row.Dimensions["direction"] == "decrease" {
			amount = amount.Neg()
		}
		bucket := row.Dimensions["bucket"]
		reportBalances[bucket] = reportBalances[bucket].Add(amount)
	}
	for _, balance := range full.Balances {
		if reportBalances[balance.Key.Bucket].StringFixed(2) != balance.Amount {
			t.Fatalf("report/ledger mismatch bucket=%s report=%s replay=%s", balance.Key.Bucket, reportBalances[balance.Key.Bucket].StringFixed(2), balance.Amount)
		}
	}
	asOf := time.Date(2026, 7, 21, 9, 2, 30, 0, time.UTC)
	partial, err := recordservice.RecordLedgerReplay(object, page.Items, &asOf, secret)
	if err != nil || partial.Entries != 3 {
		t.Fatalf("partial replay=%#v err=%v", partial, err)
	}
	gymLedgerP6AssertBalances(t, partial, map[string]string{"bonus": "20.00", "principal": "70.00"})

	if _, err := store.DB().ExecContext(t.Context(), `UPDATE gym_financial_ledger SET source_reference = ? WHERE workspace_id = ? AND id = ?`, "tampered-source", "default", "ledger-4"); err != nil {
		t.Fatal(err)
	}
	tampered, err := repository.ListRecords(t.Context(), "default", object, recordmodel.RecordListQuery{Page: 1, PageSize: 20, Sort: []recordmodel.RecordSortRule{{Field: "sequence", Direction: "asc"}}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = recordservice.RecordLedgerReplay(object, tampered.Items, nil, secret)
	ledgerErr, ok := err.(*recordservice.RecordLedgerError)
	if !ok || ledgerErr.Code != "backend.ledger.integrity_mismatch" || ledgerErr.RecordID != "ledger-4" || ledgerErr.Sequence != 4 {
		t.Fatalf("tamper error=%#v", err)
	}
}

func gymLedgerP6Object() definitionmodel.ObjectSchema {
	fields := []definitionmodel.FieldSchema{}
	for _, key := range []string{"account_id", "business_key", "entry_kind", "direction", "balance_bucket", "currency", "source_reference", "actor_id", "rule_version", "reversal_of", "previous_hash", "entry_hash", "signature"} {
		fields = append(fields, definitionmodel.FieldSchema{Key: key, Type: "text", Required: key != "reversal_of"})
	}
	fields = append(fields, definitionmodel.FieldSchema{Key: "amount", Type: "currency", Required: true, Config: map[string]any{"precision": 19, "scale": 2, "currency_code": "CNY"}}, definitionmodel.FieldSchema{Key: "occurred_at", Type: "datetime", Required: true}, definitionmodel.FieldSchema{Key: "sequence", Type: "number", Required: true})
	return definitionmodel.ObjectSchema{Key: "gym_financial_ledger", Fields: fields, LifecyclePolicy: &definitionmodel.ObjectLifecyclePolicy{Mode: definitionmodel.ObjectLifecycleAppendOnly}, LedgerPolicy: &definitionmodel.ObjectLedgerPolicy{Integrity: definitionmodel.ObjectLedgerIntegritySHA256Chain, Signature: definitionmodel.ObjectLedgerSignatureHMACSHA256}}
}

func gymLedgerP6Data(businessKey, kind, direction, bucket, amount, source string, sequence int, occurredAt, reversalOf string) map[string]any {
	return map[string]any{"account_id": "member-account-1", "business_key": businessKey, "entry_kind": kind, "direction": direction, "balance_bucket": bucket, "amount": amount, "currency": "CNY", "source_reference": source, "actor_id": "finance-operator", "rule_version": "3", "occurred_at": occurredAt, "sequence": sequence, "reversal_of": reversalOf}
}

func gymLedgerP6AssertBalances(t *testing.T, result recordmodel.RecordLedgerReplayResult, want map[string]string) {
	t.Helper()
	got := map[string]string{}
	for _, balance := range result.Balances {
		if balance.Key.AccountID != "member-account-1" || balance.Key.Currency != "CNY" {
			t.Fatalf("unexpected balance key=%#v", balance.Key)
		}
		got[balance.Key.Bucket] = balance.Amount
	}
	if len(got) != len(want) {
		t.Fatalf("balances=%v want=%v", got, want)
	}
	for key, amount := range want {
		if got[key] != amount {
			t.Fatalf("balance %s=%s want=%s", key, got[key], amount)
		}
	}
}
