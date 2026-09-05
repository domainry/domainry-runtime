package reportmodulehost

import (
	"context"
	"strings"
	"testing"

	reportmodel "github.com/domainry/domainry-report-sdk/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

type objectSQLSourceAccessProbe struct {
	objects map[string]definitionmodel.ObjectSchema
	reads   []string
}

func (p *objectSQLSourceAccessProbe) ReportObjectForAction(_ context.Context, _ principalmodel.Principal, objectKey, action string) (definitionmodel.ObjectSchema, error) {
	p.reads = append(p.reads, objectKey+"."+action)
	return p.objects[objectKey], nil
}

func (*objectSQLSourceAccessProbe) NormalizeReportListQuery(_ context.Context, _ definitionmodel.ObjectSchema, query recordmodel.RecordListQuery, _ principalmodel.Principal) recordmodel.RecordListQuery {
	return query
}

func (*objectSQLSourceAccessProbe) CanAccessReportRecord(context.Context, principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool {
	return true
}

func TestReportModuleHostDiscoversAndAuthorizesSQLOwnedSources(t *testing.T) {
	access := &objectSQLSourceAccessProbe{objects: map[string]definitionmodel.ObjectSchema{
		"sale": {Key: "sale"},
		"payment": {Key: "payment", Fields: []definitionmodel.FieldSchema{{
			Key: "sale_id", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "sale"}, Config: map[string]any{"cardinality": "many_to_one"},
		}}},
	}}
	host := NewReportModuleQueryHost(ReportModuleQueryHostDependencies{Access: access})
	report := reportmodel.ReportSchema{ObjectSQLV1: &reportmodel.ReportObjectSQLSchema{
		SQL: `SELECT s.id AS sale_id FROM sale s LEFT JOIN payment p ON p.sale_id = s.id`,
	}}
	sources, err := host.ResolveReportObjectSQLSources(t.Context(), report, reportmodel.ReportSubject{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 2 || strings.Join(access.reads, ",") != "sale.read,payment.read" {
		t.Fatalf("sources=%#v reads=%v", sources, access.reads)
	}
	field := sources["payment"].Fields[0]
	if field.RelationTarget != "sale" || field.RelationCardinality != "many_to_one" {
		t.Fatalf("field=%#v", field)
	}
}
