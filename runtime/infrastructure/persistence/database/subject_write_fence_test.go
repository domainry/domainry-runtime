package database

import (
	"strings"
	"testing"

	ormdialect "github.com/domainry/domainry-orm/dialect"
	"github.com/domainry/domainry-orm/query"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/base"
)

func TestSubjectInsertKeepsWorkspaceAndOwnershipBoundAcrossSQLDialects(t *testing.T) {
	for _, name := range []ormdialect.Name{ormdialect.SQLite, ormdialect.Postgres, ormdialect.MySQL} {
		t.Run(string(name), func(t *testing.T) {
			dialect, err := ormdialect.New(name)
			if err != nil {
				t.Fatal(err)
			}
			renderer := dialect.WithSchema("runtime_schema")
			store := &RuntimeStore{SQLDatabase: &base.SQLDatabase{SQLRenderer: renderer}}
			builder, err := store.SubjectEvidenceInsertBuilder("workspace-a", "_automation_rule_executions",
				[]string{"id", "actor_id", "object_key", "record_id", "candidate_json"},
				[]any{"execution-1", "alice", "member_profile", "profile-alice", `{"name":"private' --"}`})
			if err != nil {
				t.Fatal(err)
			}
			statement, args, err := builder.Build()
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(statement, "INSERT INTO "+renderer.Table("_automation_rule_executions")+" ("+renderer.Identifier("workspace_id")) || !strings.Contains(statement, "NOT (EXISTS") || strings.Contains(statement, "private") || len(args) < 6 || args[0] != "workspace-a" || args[5] != `{"name":"private' --"}` {
				t.Fatalf("ownership insert lost scope or bindings: %s %#v", statement, args)
			}
			for _, value := range []string{"subject", "record", "profile-alice", "alice"} {
				found := false
				for _, arg := range args[6:] {
					found = found || arg == value
				}
				if !found {
					t.Fatal("ownership binding missing", value)
				}
			}
			update, _, err := query.NewWorkspaceUpdateBuilder(renderer, "_automation_instruction_executions", "workspace-a").Set("result_json", "{}").Where(store.SubjectEvidenceRowWriteAllowed("workspace-a", "_automation_instruction_executions")).Build()
			if err != nil || !strings.Contains(update, renderer.Table("_automation_instruction_executions")+"."+renderer.Identifier("id")) {
				t.Fatalf("fence lost actual row correlation: %s %v", update, err)
			}
			if _, err := store.SubjectEvidenceInsertBuilder("", "member_profile", []string{"id"}, []any{"alice"}); err == nil {
				t.Fatal("empty workspace accepted")
			}
			if _, err := store.SubjectEvidenceInsertBuilder("workspace-a", "member_profile", []string{"workspace_id"}, []any{"workspace-b"}); err == nil {
				t.Fatal("caller could override insert workspace")
			}
		})
	}
}
