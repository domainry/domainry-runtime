package capacity

import "testing"

func TestFairOrderPreventsHotWorkspaceMonopoly(t *testing.T) {
	type item struct{ workspace, id string }
	items := []item{{"hot", "1"}, {"hot", "2"}, {"hot", "3"}, {"other", "1"}}
	ordered := FairOrder(items, 3, func(value item) string { return value.workspace })
	if len(ordered) != 3 || ordered[0].workspace != "hot" || ordered[1].workspace != "other" || ordered[2].workspace != "hot" {
		t.Fatalf("unfair order: %#v", ordered)
	}
	if got := OverscanLimit(200, 4, 800); got != 800 {
		t.Fatalf("overscan=%d", got)
	}
}

func TestQuotaFairOrderReservesClassesAndReusesIdleQuota(t *testing.T) {
	type item struct{ workspace, class string }
	items := []item{{"hot", "retry"}, {"hot", "retry"}, {"hot", "retry"}, {"a", "new"}, {"b", "new"}, {"c", "manual"}}
	ordered := QuotaFairOrder(items, 4, func(value item) string { return value.workspace }, func(value item) string { return value.class }, []string{"retry", "manual", "new"}, map[string]int{"retry": 1, "manual": 1, "new": 2})
	counts := map[string]int{}
	for _, value := range ordered {
		counts[value.class]++
	}
	if len(ordered) != 4 || counts["retry"] != 1 || counts["manual"] != 1 || counts["new"] != 2 {
		t.Fatalf("quota ordering=%#v counts=%#v", ordered, counts)
	}
	withoutManual := QuotaFairOrder(items[:5], 4, func(value item) string { return value.workspace }, func(value item) string { return value.class }, []string{"retry", "manual", "new"}, map[string]int{"retry": 1, "manual": 1, "new": 2})
	if len(withoutManual) != 4 {
		t.Fatalf("idle quota was not reused: %#v", withoutManual)
	}
}
