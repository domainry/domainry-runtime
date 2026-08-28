package metadata

import (
	"database/sql"
	"testing"
)

func TestMetadataLocalizedProjectionBranches(t *testing.T) {
	baseDB := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = baseDB.Close() })
	base := NewMetadataStore(baseDB)
	run := func(t *testing.T, state metadataSQLState, payload string) error {
		t.Helper()
		return runMetadataTransaction(t, base, state, func(repository MetadataStore, tx *sql.Tx) error {
			return repository.syncMetadataLocalizedTextTx(t.Context(), tx, " object ", " account ", []byte(payload), "source", "now")
		})
	}
	if err := run(t, metadataSQLState{}, `{`); err == nil {
		t.Fatal("expected projection decode error")
	}
	if err := run(t, metadataSQLState{}, `{}`); err != nil {
		t.Fatal(err)
	}
	payload := `{"i18n":{"en-US":{"name":"Account"}}}`
	for _, testCase := range []struct {
		steps []metadataSQLExecStep
		err   bool
	}{
		{steps: []metadataSQLExecStep{{err: errMetadataSQL}}, err: true},
		{steps: []metadataSQLExecStep{{rows: 1}, {err: errMetadataSQL}}, err: true},
		{steps: []metadataSQLExecStep{{rows: 1}, {rowsErr: errMetadataSQL}}, err: true},
		{steps: []metadataSQLExecStep{{rows: 1}, {rows: 1}}},
		{steps: []metadataSQLExecStep{{rows: 1}, {rows: 0}, {err: errMetadataSQL}}, err: true},
		{steps: []metadataSQLExecStep{{rows: 1}, {rows: 0}, {rows: 1}}},
	} {
		if err := run(t, metadataSQLState{execSteps: testCase.steps}, payload); (err != nil) != testCase.err {
			t.Fatalf("steps=%#v err=%v", testCase.steps, err)
		}
	}
}

func TestMetadataLocalizedProjectionShapeBranches(t *testing.T) {
	if metadataLocalizedProjections("invalid") != nil {
		t.Fatal("invalid projection shape accepted")
	}
	projections := metadataLocalizedProjections(map[string]any{
		"bad":   "properties",
		"":      map[string]any{"name": "ignored"},
		"en-US": map[string]any{"": "ignored", "empty": " ", "number": 1, "name": "Account", "description": "Description"},
	})
	if len(projections) != 2 || projections[0].Property != "description" || projections[1].Property != "name" {
		t.Fatalf("projections=%#v", projections)
	}
}
