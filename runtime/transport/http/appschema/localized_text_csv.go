package appschema

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/xml"
	"fmt"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	"io"
	"strings"
)

var localizedTextCSVColumns = []string{"workspace_id", "entity_type", "entity_key", "property", "locale", "text"}

func localizedTextCSV(values []appschemamodel.LocalizedText) []byte {
	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)
	_ = writer.Write(localizedTextCSVColumns)
	for _, value := range values {
		_ = writer.Write([]string{
			value.WorkspaceID,
			value.EntityType,
			value.EntityKey,
			value.Property,
			value.Locale,
			value.Text,
		})
	}
	writer.Flush()
	return buf.Bytes()
}

func localizedTextXLSX(values []appschemamodel.LocalizedText) []byte {
	var buf bytes.Buffer
	archive := zip.NewWriter(&buf)
	files := map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
<Default Extension="xml" ContentType="application/xml"/>
<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>
<Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>
</Types>`,
		"_rels/.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>
</Relationships>`,
		"xl/workbook.xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
<sheets><sheet name="localized_texts" sheetId="1" r:id="rId1"/></sheets>
</workbook>`,
		"xl/_rels/workbook.xml.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/>
</Relationships>`,
		"xl/worksheets/sheet1.xml": localizedTextWorksheetXML(values),
	}
	for _, name := range []string{"[Content_Types].xml", "_rels/.rels", "xl/workbook.xml", "xl/_rels/workbook.xml.rels", "xl/worksheets/sheet1.xml"} {
		writer, _ := archive.Create(name)
		_, _ = writer.Write([]byte(files[name]))
	}
	_ = archive.Close()
	return buf.Bytes()
}

func localizedTextWorksheetXML(values []appschemamodel.LocalizedText) string {
	rows := make([][]string, 0, len(values)+1)
	rows = append(rows, localizedTextCSVColumns)
	for _, value := range values {
		rows = append(rows, []string{
			value.WorkspaceID,
			value.EntityType,
			value.EntityKey,
			value.Property,
			value.Locale,
			value.Text,
		})
	}
	var buf bytes.Buffer
	buf.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	buf.WriteString(`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`)
	for rowIndex, row := range rows {
		buf.WriteString(fmt.Sprintf(`<row r="%d">`, rowIndex+1))
		for columnIndex, value := range row {
			cellRef := spreadsheetColumnName(columnIndex+1) + fmt.Sprint(rowIndex+1)
			buf.WriteString(`<c r="`)
			buf.WriteString(cellRef)
			buf.WriteString(`" t="inlineStr"><is><t>`)
			_ = xml.EscapeText(&buf, []byte(value))
			buf.WriteString(`</t></is></c>`)
		}
		buf.WriteString(`</row>`)
	}
	buf.WriteString(`</sheetData></worksheet>`)
	return buf.String()
}

func spreadsheetColumnName(index int) string {
	if index <= 0 {
		return "A"
	}
	name := ""
	for index > 0 {
		index--
		name = string(rune('A'+index%26)) + name
		index /= 26
	}
	return name
}

func localizedTextUpsertRequestsFromCSV(reader io.Reader) ([]appschemamodel.LocalizedTextUpsertRequest, error) {
	csvReader := csv.NewReader(reader)
	csvReader.TrimLeadingSpace = true
	csvReader.FieldsPerRecord = -1
	rows, err := csvReader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("read csv: %w", err)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("csv header is required")
	}
	header := map[string]int{}
	for index, column := range rows[0] {
		header[strings.TrimSpace(column)] = index
	}
	for _, column := range localizedTextCSVColumns {
		if _, ok := header[column]; !ok {
			return nil, fmt.Errorf("csv column %q is required", column)
		}
	}
	requests := make([]appschemamodel.LocalizedTextUpsertRequest, 0, len(rows)-1)
	for rowIndex, row := range rows[1:] {
		get := func(column string) string {
			index := header[column]
			if index >= len(row) {
				return ""
			}
			return strings.TrimSpace(row[index])
		}
		req := appschemamodel.LocalizedTextUpsertRequest{
			WorkspaceID: get("workspace_id"),
			EntityType:  get("entity_type"),
			EntityKey:   get("entity_key"),
			Property:    get("property"),
			Locale:      get("locale"),
			Text:        get("text"),
			SourceKind:  "user",
			SourceID:    "metadata_csv_import",
		}
		if req.EntityType == "" || req.EntityKey == "" || req.Property == "" || req.Locale == "" || req.Text == "" {
			return nil, fmt.Errorf("csv row %d requires entity_type, entity_key, property, locale, and text", rowIndex+2)
		}
		requests = append(requests, req)
	}
	return requests, nil
}

func validateLocalizedTextUpsertRequests(requests []appschemamodel.LocalizedTextUpsertRequest, allowed map[string]bool) error {
	if len(allowed) == 0 {
		return fmt.Errorf("no localized text schema keys are available")
	}
	for index, req := range requests {
		key := localizedTextImportKey(req.EntityType, req.EntityKey, req.Property)
		if !allowed[key] {
			return fmt.Errorf("csv row %d references unknown %s/%s/%s", index+2, strings.TrimSpace(req.EntityType), strings.TrimSpace(req.EntityKey), strings.TrimSpace(req.Property))
		}
	}
	return nil
}

func localizedTextImportKey(entityType string, entityKey string, property string) string {
	return strings.TrimSpace(entityType) + "\x00" + strings.TrimSpace(entityKey) + "\x00" + strings.TrimSpace(property)
}
