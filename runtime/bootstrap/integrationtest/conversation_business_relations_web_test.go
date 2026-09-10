package integrationtest

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func businessRelationsManifest(manifest map[string]any) {
	field := func(key, kind string) map[string]any { return map[string]any{"key": key, "name": key, "type": kind} }
	ref := func(key, target string) map[string]any {
		f := field(key, "relation")
		f["config"] = map[string]any{"object_key": target}
		return f
	}
	manifest["objects"] = append(manifest["objects"].([]any),
		map[string]any{"key": "order", "name": "订单", "fields": []any{field("name", "text"), field("amount", "integer"), ref("customer", "customer"), ref("project", "project")}},
		map[string]any{"key": "project", "name": "项目", "fields": []any{field("name", "text"), ref("customer", "customer")}},
	)
	for _, item := range manifest["roles"].([]any) {
		role := item.(map[string]any)
		if role["key"] != "headquarters_admin" && role["key"] != "business_restricted" && role["key"] != "business_field_restricted" {
			continue
		}
		for _, key := range []string{"order.read", "project.read", agentsdk.ConversationToolActionPrefix + "query_related_records"} {
			scope := "owner"
			if strings.HasPrefix(key, agentsdk.ConversationToolActionPrefix) {
				scope = "all"
			}
			role["permissions"] = append(role["permissions"].([]any), map[string]any{"permission_key": key, "data_scope": scope})
		}
		for object, keys := range map[string][]string{"order": {"name", "amount", "customer", "project"}, "project": {"name", "customer"}} {
			for _, key := range keys {
				read := !(role["key"] == "business_field_restricted" && object == "order" && key == "customer")
				role["field_permissions"] = append(role["field_permissions"].([]any), map[string]any{"object_key": object, "field_key": key, "read": read, "write": false, "export": false})
			}
		}
	}
	customerSeed := ""
	for _, item := range manifest["seed_records"].([]any) {
		seed := item.(map[string]any)
		data := seed["data"].(map[string]any)
		if seed["object_key"] == "customer" && data["name"] == "Acme" {
			customerSeed, _ = data["__seed_key"].(string)
		}
	}
	if customerSeed == "" {
		panic("relation fixture requires the actual Acme seed key")
	}
	seed := func(object, key, owner string, data map[string]any) map[string]any {
		data["__seed_key"] = key
		return map[string]any{"object_key": object, "owner_user_id": owner, "data": data}
	}
	manifest["seed_records"] = append(manifest["seed_records"].([]any),
		seed("project", "relation_project", "admin", map[string]any{"name": "Delivery Project", "customer": "$record:" + customerSeed}),
		seed("order", "relation_order_a", "admin", map[string]any{"name": "Order Alpha", "amount": 100, "customer": "$record:" + customerSeed, "project": "$record:relation_project"}),
		seed("order", "relation_order_b", "admin", map[string]any{"name": "Order Beta", "amount": 200, "customer": "$record:" + customerSeed, "project": "$record:relation_project"}),
		seed("order", "relation_order_hidden", "restricted_user", map[string]any{"name": "PRIVATE-ORDER", "amount": 900, "customer": "$record:" + customerSeed}),
	)
}

func TestConversationBusinessRelationsThroughIdentityWebAndRestart(t *testing.T) {
	f := newBusinessWebFixture(t, businessRelationsManifest)
	if os.Getenv("RUNTIME_BUSINESS_LIVE") != "1" {
		f.close()
		f.options.ConversationProvider = businessRelationsWebModel{}
		f.open()
	}
	b := &businessBrowser{t: t, f: f, cookies: map[string]*http.Cookie{}}
	b.call("POST", "/auth/login", map[string]any{"login": "admin@example.com", "password": "Business-Browser-Initial!2026"}, 200)
	b.call("POST", "/auth/password/change", map[string]any{"current_password": "Business-Browser-Initial!2026", "new_password": businessWebPassword}, 200)
	b.assign("headquarters_admin")
	b.session()
	var c agentsdk.Conversation
	if err := json.Unmarshal(b.call("POST", "/agent/conversations", map[string]any{"client_id": "relation-acceptance", "title": "客户订单项目关联验收"}, 200).Body.Bytes(), &c); err != nil {
		t.Fatal(err)
	}
	run := b.runWithMessage(c.ID, "请发现业务对象和关系，查找 Acme 客户。通过关联查询工具按金额升序、每页一条查完它的订单，再从第一条订单查对应项目，最后从项目查回客户。列出订单名称和金额、项目名称、返回的客户名称。所有关系键和记录 ID 都必须从工具实际结果取得，不能只按名称猜关联。")
	// Keep synthetic public evidence for failed live acceptance as well.
	if dir := os.Getenv("RUNTIME_BUSINESS_EVIDENCE_DIR"); dir != "" {
		raw, err := json.MarshalIndent(run, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, "business-relations-run.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	related, catalogs := 0, 0
	for _, step := range run.Steps {
		for _, call := range step.Calls {
			if call.ErrorCode != "" {
				t.Fatal("relation call failed", call.Name, call.Arguments, call.ErrorCode)
			}
			if call.Name == "query_related_records" {
				related++
			}
			if call.Name == "business_catalog" {
				catalogs++
			}
		}
	}
	if related < 4 || catalogs < 2 {
		t.Fatal("missing actual relation discovery and traversal", related, catalogs)
	}
	check := func(hidden bool) {
		t.Helper()
		var page agentsdk.ConversationMessagePage
		if err := json.Unmarshal(b.call("GET", "/agent/conversations/"+c.ID+"/messages", nil, 200).Body.Bytes(), &page); err != nil || len(page.Items) != 2 {
			t.Fatal(page, err)
		}
		message := page.Items[1]
		if hidden {
			if message.AccessError == "" || strings.Contains(message.Content, "Order Alpha") {
				t.Fatal("revoked relationship remained visible", message)
			}
			return
		}
		for _, fact := range []string{"Order Alpha", "Order Beta", "100", "200", "Delivery Project", "Acme"} {
			if !strings.Contains(message.Content, fact) {
				t.Fatal("relation fact omitted", fact, message)
			}
		}
		if strings.Contains(message.Content, "PRIVATE-") || message.AccessError != "" {
			t.Fatal("relation scope lost", message)
		}
	}
	check(false)
	f.close()
	f.open()
	b.session()
	check(false)
	for _, role := range []string{"business_field_restricted", "headquarters_admin", "business_restricted", "headquarters_admin"} {
		b.assign(role)
		b.session()
		check(role != "headquarters_admin")
	}
	t.Logf("Relation HTTP passed: conversation=%s related=%d catalogs=%d; customer/order/project, full restart, relation-field and source-read revocation", c.ID, related, catalogs)
	if os.Getenv("RUNTIME_BUSINESS_BROWSER") == "1" {
		serveBusinessBrowserAcceptance(t, f, b, c.ID)
	}
}
