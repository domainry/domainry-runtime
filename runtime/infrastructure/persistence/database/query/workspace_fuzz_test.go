package query

import (
	"fmt"
	"strings"
	"testing"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func FuzzTenantQueryWorkspaceCannotBeOverridden(f *testing.F) {
	for _, seed := range []string{"workspace-b", "*", "../workspace-b", "' OR 1=1 --", "workspace-a\x00workspace-b"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, injected string) {
		where, args, err := BuildTenantWhere(fuzzQueryStore{}, "workspace-a", recordmodel.RecordListQuery{
			Scope:   "all_records",
			Filters: map[string]any{"workspace_id": injected, "related_id": injected},
			Sort:    []recordmodel.RecordSortRule{{Field: injected, Direction: injected}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(args) == 0 || args[0] != "workspace-a" || strings.Count(where, `"workspace_id"`) < 1 {
			t.Fatalf("workspace boundary lost: where=%s args=%#v", where, args)
		}
		for _, arg := range args {
			if fmt.Sprint(arg) == "workspace-b" && args[0] != arg {
				// A hostile value may remain a parameter for a non-workspace field;
				// it must never replace the first tenant argument.
				continue
			}
		}
	})
}

type fuzzQueryStore struct{}

func (fuzzQueryStore) Identifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}
func (fuzzQueryStore) TableIdentifier(value string) string { return fuzzQueryStore{}.Identifier(value) }
func (fuzzQueryStore) Placeholder(position int) string     { return fmt.Sprintf("$%d", position) }
