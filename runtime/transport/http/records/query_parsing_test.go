package records

import (
	"net/http/httptest"
	"reflect"
	"testing"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestParseListQueryNormalizesPaginationFiltersAndSort(t *testing.T) {
	r := httptest.NewRequest("GET", "/records?page=2&page_size=25&search=+needle+&view=+compact+&filters=%7B%22status%22%3A%22open%22%7D&sort=-created_at,name+desc,priority:DESC,empty:", nil)
	query := parseListQuery(r)
	if query.Page != 2 || query.PageSize != 25 || query.Search != "needle" || query.ViewKey != "compact" || query.Filters["status"] != "open" {
		t.Fatalf("query=%#v", query)
	}
	want := []recordmodel.RecordSortRule{
		{Field: "created_at", Direction: "desc"},
		{Field: "name", Direction: "desc"},
		{Field: "priority", Direction: "DESC"},
		{Field: "empty", Direction: ""},
	}
	if !reflect.DeepEqual(query.Sort, want) {
		t.Fatalf("sort=%#v want=%#v", query.Sort, want)
	}
}

func TestParseListQueryIgnoresMalformedFiltersAndEmptySortItems(t *testing.T) {
	r := httptest.NewRequest("GET", "/records?filters=%7B&sort=+,+name,+", nil)
	query := parseListQuery(r)
	if len(query.Filters) != 0 || !reflect.DeepEqual(query.Sort, []recordmodel.RecordSortRule{{Field: "name", Direction: "asc"}}) {
		t.Fatalf("query=%#v", query)
	}
	if intQuery(" invalid ") != 0 || intQuery(" 7 ") != 7 {
		t.Fatal("integer parsing mismatch")
	}
	if got := splitQueryCSV(" one, , two "); !reflect.DeepEqual(got, []string{"one", "two"}) {
		t.Fatalf("csv=%#v", got)
	}
}
