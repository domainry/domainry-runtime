package integrationtest

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-orm/dialect"
	"github.com/domainry/domainry-orm/query"
)

// This test-only offline conversion runs with the fixture Runtime stopped.
// It restores the old source-publication format in the same host SQLite file,
// leaving actual model executions and immutable agreement/delivery history
// untouched. Production modules continue to borrow the host connection.
func downgradeMixedSourcePublicationFixture(t *testing.T, f *businessWebFixture, delegationID string, deliveryOnly ...bool) {
	t.Helper()
	if f.runtime != nil || f.cfg.DatabaseDriver != "sqlite" {
		t.Fatal("legacy fixture conversion requires a stopped SQLite Runtime")
	}
	db, err := sql.Open("sqlite", f.cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	renderer, err := dialect.ParseRenderer("sqlite", "", "")
	if err != nil {
		t.Fatal(err)
	}
	q, args, err := query.NewSelectBuilder(renderer, "_agent_source_releases").Columns("owner_key", "release_id", "payload_json").Where(query.Equal("delegation_id", delegationID)).Build()
	if err != nil {
		t.Fatal(err)
	}
	rows, err := db.QueryContext(t.Context(), q, args...)
	if err != nil {
		t.Fatal(err)
	}
	type publication struct {
		owner, id string
		release   persistence.ConversationSourceRelease
	}
	items := []publication{}
	deliveries := 0
	contracts := 0
	for rows.Next() {
		var item publication
		var raw []byte
		if err := rows.Scan(&item.owner, &item.id, &raw); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &item.release); err != nil || item.release.Publisher == nil || item.release.Publisher.RoleKey != "a_results_reader" {
			t.Fatal("fixture did not record the actual publication role", item.release, err)
		}
		if item.release.Purpose == "delivery" {
			deliveries++
		} else if item.release.Purpose == "contract" {
			if len(deliveryOnly) > 0 && deliveryOnly[0] {
				continue
			}
			contracts++
		} else {
			continue
		}
		item.release.Publisher = nil
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if deliveries < 2 || (len(deliveryOnly) == 0 || !deliveryOnly[0]) && contracts < 2 {
		t.Fatal("fixture did not publish both original professional contract/delivery roots", contracts, deliveries)
	}
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, item := range items {
		raw, err := json.Marshal(item.release)
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(raw)
		q, args, err := query.NewUpdateBuilder(renderer, "_agent_source_releases").Set("release_id", hex.EncodeToString(digest[:])).Set("payload_json", raw).Where(query.And(query.Equal("owner_key", item.owner), query.Equal("release_id", item.id))).Build()
		if err != nil {
			t.Fatal(err)
		}
		result, err := tx.ExecContext(t.Context(), q, args...)
		if err != nil {
			t.Fatal(err)
		}
		if n, err := result.RowsAffected(); err != nil || n != 1 {
			t.Fatal("legacy conversion missed publication", n, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
