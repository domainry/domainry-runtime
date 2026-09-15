package coderuntime

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	lua "github.com/yuin/gopher-lua"
)

const (
	workerMaxSourceBytes = 32 * 1024
	workerMaxDepth       = 32
	workerMaxItems       = 10000
)

func RunWorker(stdin io.Reader, stdout, stderr io.Writer) int {
	encoder := json.NewEncoder(stdout)
	decoder := json.NewDecoder(bufio.NewReaderSize(stdin, 64*1024))
	var incoming frame
	if decoder.Decode(&incoming) != nil || incoming.Type != "execute" || incoming.Execute == nil {
		_ = encoder.Encode(frame{Type: "error", ErrorCode: "code_protocol_failed"})
		return 2
	}
	result, code := executeWorker(incoming.Execute, decoder, encoder)
	if code != "" {
		_ = encoder.Encode(frame{Type: "error", ErrorCode: code})
		return 1
	}
	if err := encoder.Encode(frame{Type: "complete", Complete: &result}); err != nil {
		_, _ = fmt.Fprintln(stderr, "code worker output failed")
		return 2
	}
	return 0
}

func executeWorker(request *agentsdk.ConversationCodeExecution, decoder *json.Decoder, encoder *json.Encoder) (agentsdk.ConversationCodeResult, string) {
	empty := agentsdk.ConversationCodeResult{}
	if request.ProtocolVersion != agentsdk.ConversationCodeProtocolVersion || request.Language != agentsdk.ConversationCodeLanguageLua || len(request.Source) == 0 || len(request.Source) > workerMaxSourceBytes || request.MaxDispatches < 1 || request.MaxDispatches > 64 || request.MaxOutputBytes < 1 || request.MaxOutputBytes > 64*1024 || request.MaxLogBytes < 0 || request.MaxLogBytes > 16*1024 || len(request.Tools) > 128 {
		return empty, "code_input_invalid"
	}
	state := lua.NewState(lua.Options{SkipOpenLibs: true, CallStackSize: 128, RegistrySize: 2048, RegistryMaxSize: 4096, MinimizeStackMemory: true})
	defer state.Close()
	state.SetMx(64)
	state.SetContext(context.Background())
	lua.OpenBase(state)
	lua.OpenTable(state)
	lua.OpenString(state)
	lua.OpenMath(state)
	for _, name := range []string{"dofile", "load", "loadfile", "loadstring", "require", "module", "collectgarbage", "getmetatable", "setmetatable", "rawget", "rawset", "rawequal", "newproxy", "print"} {
		state.SetGlobal(name, lua.LNil)
	}
	if mathTable, ok := state.GetGlobal("math").(*lua.LTable); ok {
		mathTable.RawSetString("random", lua.LNil)
		mathTable.RawSetString("randomseed", lua.LNil)
	}
	if stringTable, ok := state.GetGlobal("string").(*lua.LTable); ok {
		stringTable.RawSetString("dump", lua.LNil)
	}
	dispatches := 0
	logs, logBytes := []string{}, 0
	toolsTable := state.NewTable()
	seenTools := map[string]bool{}
	for _, definition := range request.Tools {
		if definition.Key == "" || definition.Key == agentsdk.ConversationCodeToolKey || seenTools[definition.Key] {
			return empty, "code_input_invalid"
		}
		seenTools[definition.Key] = true
		name := definition.Key
		state.SetField(toolsTable, name, state.NewFunction(func(L *lua.LState) int {
			if dispatches >= request.MaxDispatches {
				L.RaiseError("code_dispatch_limit")
				return 0
			}
			arguments := map[string]any{}
			if L.GetTop() > 0 && L.Get(1) != lua.LNil {
				if table, ok := L.Get(1).(*lua.LTable); ok && luaTableEmpty(table) {
					arguments = map[string]any{}
				} else {
					converted, err := luaToJSON(L.Get(1), 0, map[*lua.LTable]bool{}, new(int))
					if err != nil {
						L.RaiseError("code_arguments_invalid")
						return 0
					}
					var ok bool
					arguments, ok = converted.(map[string]any)
					if !ok {
						L.RaiseError("code_arguments_invalid")
						return 0
					}
				}
			}
			raw, err := json.Marshal(arguments)
			if err != nil {
				L.RaiseError("code_arguments_invalid")
				return 0
			}
			dispatch := agentsdk.ConversationCodeDispatch{Index: dispatches, Name: name, Arguments: raw}
			if err = encoder.Encode(frame{Type: "dispatch", Dispatch: &dispatch}); err != nil {
				L.RaiseError("code_protocol_failed")
				return 0
			}
			var reply frame
			if decoder.Decode(&reply) != nil || reply.Type != "result" || reply.Result == nil {
				L.RaiseError("code_protocol_failed")
				return 0
			}
			dispatches++
			envelope := map[string]any{"status": reply.Result.Status}
			if len(reply.Result.Content) > 0 {
				var content any
				if json.Unmarshal(reply.Result.Content, &content) != nil {
					L.RaiseError("code_tool_result_invalid")
					return 0
				}
				envelope["value"] = content
			}
			if reply.Result.ErrorCode != "" {
				envelope["error_code"] = reply.Result.ErrorCode
			}
			if reply.Result.Completion != "" {
				envelope["completion"] = reply.Result.Completion
			}
			if reply.Result.ResourceID != "" {
				envelope["resource_id"] = reply.Result.ResourceID
			}
			value, err := jsonToLua(L, envelope, 0)
			if err != nil {
				L.RaiseError("code_tool_result_invalid")
				return 0
			}
			L.Push(value)
			return 1
		}))
	}
	state.SetGlobal("tools", toolsTable)
	state.SetGlobal("log", state.NewFunction(func(L *lua.LState) int {
		if len(logs) >= 128 {
			L.RaiseError("code_log_limit")
			return 0
		}
		value, err := luaToJSON(L.Get(1), 0, map[*lua.LTable]bool{}, new(int))
		if err != nil {
			L.RaiseError("code_log_invalid")
			return 0
		}
		raw, _ := json.Marshal(value)
		if len(raw) > 2048 || logBytes+len(raw) > request.MaxLogBytes {
			L.RaiseError("code_log_limit")
			return 0
		}
		logs, logBytes = append(logs, string(raw)), logBytes+len(raw)
		return 0
	}))
	function, err := state.Load(strings.NewReader(request.Source), "agent-code")
	if err != nil {
		return empty, "code_syntax_error"
	}
	base := state.GetTop()
	state.Push(function)
	if err = state.PCall(0, lua.MultRet, nil); err != nil {
		message := err.Error()
		for _, code := range []string{"code_dispatch_limit", "code_arguments_invalid", "code_protocol_failed", "code_tool_result_invalid", "code_log_limit", "code_log_invalid"} {
			if strings.Contains(message, code) {
				return empty, code
			}
		}
		return empty, "code_runtime_error"
	}
	if state.GetTop()-base != 1 {
		return empty, "code_result_required"
	}
	items := 0
	value, err := luaToJSON(state.Get(-1), 0, map[*lua.LTable]bool{}, &items)
	if err != nil {
		return empty, "code_result_invalid"
	}
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > request.MaxOutputBytes {
		return empty, "code_output_exceeded"
	}
	return agentsdk.ConversationCodeResult{Language: request.Language, Value: raw, Logs: logs, Dispatches: dispatches}, ""
}

func luaTableEmpty(table *lua.LTable) bool {
	empty := true
	table.ForEach(func(lua.LValue, lua.LValue) { empty = false })
	return empty
}

func luaToJSON(value lua.LValue, depth int, visiting map[*lua.LTable]bool, items *int) (any, error) {
	if depth > workerMaxDepth || *items > workerMaxItems {
		return nil, fmt.Errorf("value limit")
	}
	*items++
	switch typed := value.(type) {
	case *lua.LNilType:
		return nil, nil
	case lua.LBool:
		return bool(typed), nil
	case lua.LString:
		return string(typed), nil
	case lua.LNumber:
		return float64(typed), nil
	case *lua.LTable:
		if visiting[typed] {
			return nil, fmt.Errorf("cyclic table")
		}
		visiting[typed] = true
		defer delete(visiting, typed)
		array := []any{}
		object := map[string]any{}
		arrayOnly, maxIndex := true, 0
		var conversionErr error
		typed.ForEach(func(key, item lua.LValue) {
			if conversionErr != nil {
				return
			}
			converted, err := luaToJSON(item, depth+1, visiting, items)
			if err != nil {
				conversionErr = err
				return
			}
			if number, ok := key.(lua.LNumber); ok && number >= 1 && number == lua.LNumber(int(number)) {
				index := int(number)
				maxIndex = max(maxIndex, index)
				for len(array) < index {
					array = append(array, nil)
				}
				array[index-1] = converted
				return
			}
			arrayOnly = false
			text, ok := key.(lua.LString)
			if !ok {
				conversionErr = fmt.Errorf("object key")
				return
			}
			object[string(text)] = converted
		})
		if conversionErr != nil {
			return nil, conversionErr
		}
		if arrayOnly && maxIndex == typed.Len() {
			return array, nil
		}
		if len(array) > 0 {
			return nil, fmt.Errorf("mixed table")
		}
		return object, nil
	default:
		return nil, fmt.Errorf("unsupported value")
	}
}

func jsonToLua(state *lua.LState, value any, depth int) (lua.LValue, error) {
	if depth > workerMaxDepth {
		return lua.LNil, fmt.Errorf("value limit")
	}
	switch typed := value.(type) {
	case nil:
		return lua.LNil, nil
	case bool:
		return lua.LBool(typed), nil
	case string:
		return lua.LString(typed), nil
	case float64:
		return lua.LNumber(typed), nil
	case []any:
		table := state.NewTable()
		for _, item := range typed {
			converted, err := jsonToLua(state, item, depth+1)
			if err != nil {
				return lua.LNil, err
			}
			table.Append(converted)
		}
		return table, nil
	case map[string]any:
		table := state.NewTable()
		for key, item := range typed {
			converted, err := jsonToLua(state, item, depth+1)
			if err != nil {
				return lua.LNil, err
			}
			table.RawSetString(key, converted)
		}
		return table, nil
	default:
		return lua.LNil, fmt.Errorf("unsupported JSON value")
	}
}
