package validation

import (
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestRecordNormalizeListQueryPaginationContract(t *testing.T) {
	tests := []struct {
		name               string
		page, pageSize     int
		wantPage, wantSize int
	}{
		{name: "zero defaults", page: 0, pageSize: 0, wantPage: 1, wantSize: 25},
		{name: "negative defaults", page: -2, pageSize: -2, wantPage: 1, wantSize: 25},
		{name: "maximum retained", page: 2, pageSize: 200, wantPage: 2, wantSize: 200},
		{name: "oversize clamps", page: 2, pageSize: 201, wantPage: 2, wantSize: 200},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			query := RecordNormalizeListQuery(definitionmodel.ObjectSchema{Key: "lead"}, recordmodel.RecordListQuery{Page: test.page, PageSize: test.pageSize})
			if query.Page != test.wantPage || query.PageSize != test.wantSize {
				t.Fatalf("pagination=(%d,%d) want=(%d,%d)", query.Page, query.PageSize, test.wantPage, test.wantSize)
			}
		})
	}
}
