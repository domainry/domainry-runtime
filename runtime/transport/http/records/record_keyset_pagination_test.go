package records

import (
	"net/http/httptest"
	"testing"
)

// TestListQueryParsesKeysetCursor pins the wire parameter that makes page 2
// reachable. The store has always required an id cursor for page > 1, but
// nothing parsed one from the request, so every caller's page=2 was refused --
// as a 500, because the refusal was a plain error. A caller must be able to
// send the cursor the previous page published.
func TestListQueryParsesKeysetCursor(t *testing.T) {
	request := httptest.NewRequest("GET", "/records/ticket?page=2&page_size=25&after_id=ticket-42&sort=id:asc", nil)
	query := parseListQuery(request)
	if query.AfterID != "ticket-42" {
		t.Fatalf("after_id was not parsed: %+v", query)
	}
	if query.Page != 2 || query.PageSize != 25 {
		t.Fatalf("page/page_size regressed: %+v", query)
	}
	if len(query.Sort) != 1 || query.Sort[0].Field != "id" {
		t.Fatalf("sort regressed: %+v", query.Sort)
	}
	if plain := parseListQuery(httptest.NewRequest("GET", "/records/ticket", nil)); plain.AfterID != "" {
		t.Fatalf("absent after_id must stay empty, got %q", plain.AfterID)
	}
	if padded := parseListQuery(httptest.NewRequest("GET", "/records/ticket?after_id=%20ticket-7%20", nil)); padded.AfterID != "ticket-7" {
		t.Fatalf("after_id must be trimmed, got %q", padded.AfterID)
	}
}
