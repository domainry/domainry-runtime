// Package catalog owns the closed Agent tool vocabulary and its static
// prerequisites. Authoring schemas, manifest validation, authorization, and
// gateway dispatch all consume this catalog.
package catalog

import (
	"sort"
	"strings"
)

const (
	ToolQueryRecords  = "query_records"
	ToolGetRecord     = "get_record"
	ToolInvokeAction  = "invoke_action"
	ToolReadRecord    = "readRecord"
	ToolCreateRecord  = "createRecord"
	ToolUpdateRecord  = "updateRecord"
	ToolDeleteRecord  = "deleteRecord"
	ToolCallConnector = "callConnector"
	ToolSendMessage   = "sendMessage"
	ToolSendEmail     = "sendEmail"
)

type Tool struct {
	Key                    string
	RequiresAllowedObjects bool
	RequiresAllowedActions bool
	Writes                 bool
}

var tools = map[string]Tool{
	ToolQueryRecords:  {Key: ToolQueryRecords, RequiresAllowedObjects: true},
	ToolGetRecord:     {Key: ToolGetRecord, RequiresAllowedObjects: true},
	ToolInvokeAction:  {Key: ToolInvokeAction, RequiresAllowedObjects: true, RequiresAllowedActions: true, Writes: true},
	ToolReadRecord:    {Key: ToolReadRecord},
	ToolCreateRecord:  {Key: ToolCreateRecord, Writes: true},
	ToolUpdateRecord:  {Key: ToolUpdateRecord, Writes: true},
	ToolDeleteRecord:  {Key: ToolDeleteRecord, Writes: true},
	ToolCallConnector: {Key: ToolCallConnector, Writes: true},
	ToolSendMessage:   {Key: ToolSendMessage, Writes: true},
	ToolSendEmail:     {Key: ToolSendEmail, Writes: true},
}

func Lookup(key string) (Tool, bool) {
	tool, exists := tools[strings.TrimSpace(key)]
	return tool, exists
}

func Keys() []string {
	result := make([]string, 0, len(tools))
	for key := range tools {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}
