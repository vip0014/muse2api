package hatch

import (
	"strings"
	"testing"
)

const quotaFlightSample = "0:{\"a\":\"$@1\",\"q\":\"\",\"i\":false}\n" +
	"1:{\"success\":true,\"subscription\":{\"tier\":{\"tierId\":\"hatch_free\",\"name\":\"Muse 免费版\",\"tierCode\":\"HATCH_FREE\",\"isPaid\":false,\"rank\":0},\"usage\":{\"state\":\"METERED\",\"percentUsed\":16,\"resetsAt\":1793467985,\"quotaStatus\":\"SUFFICIENT\"},\"statusSubtitle\":\"每周限额将在 10月31日重置\",\"usageRowLabel\":\"免费版\",\"usageRowValueLabel\":\"已使用 16%\",\"topupBalance\":40000000,\"topupTotal\":40000000,\"topupRowLabel\":\"额外的使用额度\",\"topupRowValueLabel\":\"已使用 0%（剩余 10 亿个词元）\",\"topupEligible\":true,\"entryPointCtaLabel\":\"升级\"}}\n"

func TestParseFlightSubscription(t *testing.T) {
	subscription, err := parseFlightSubscription([]byte(quotaFlightSample))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	quota, err := quotaFromSubscription(subscription)
	if err != nil {
		t.Fatalf("map: %v", err)
	}
	if !quota.Found || quota.Plan != "Muse 免费版" {
		t.Fatalf("unexpected plan: %+v", *quota)
	}
	if quota.WeeklyUsedPct != 16 {
		t.Fatalf("weekly used = %v", quota.WeeklyUsedPct)
	}
	if quota.WeeklyReset != "每周限额将在 10月31日重置" {
		t.Fatalf("reset = %q", quota.WeeklyReset)
	}
	if quota.ExtraUsedPct == nil || *quota.ExtraUsedPct != 0 {
		t.Fatalf("extra used = %v", quota.ExtraUsedPct)
	}
	if quota.ExtraLeft != "已使用 0%（剩余 10 亿个词元）" {
		t.Fatalf("extra left = %q", quota.ExtraLeft)
	}
}

func TestParseFlightSubscriptionFailure(t *testing.T) {
	body := "0:{\"a\":\"$@1\"}\n1:{\"success\":false,\"message\":\"unauthenticated\"}\n"
	if _, err := parseFlightSubscription([]byte(body)); err == nil || !strings.Contains(err.Error(), "unauthenticated") {
		t.Fatalf("expected upstream failure, got %v", err)
	}
	html := "<!DOCTYPE html><html><body>Sign in to continue</body></html>"
	if _, err := parseFlightSubscription([]byte(html)); err == nil {
		t.Fatal("expected error for html body")
	}
}

func TestQuotaFromSubscriptionPartial(t *testing.T) {
	subscription := map[string]any{
		"tier":         map[string]any{"tierId": "hatch_plus"},
		"topupTotal":   float64(0),
		"topupBalance": float64(0),
	}
	quota, err := quotaFromSubscription(subscription)
	if err != nil {
		t.Fatalf("map: %v", err)
	}
	if quota.Plan != "hatch_plus" || quota.ExtraUsedPct != nil {
		t.Fatalf("unexpected quota: %+v", *quota)
	}
}

func TestActionIDFromChunk(t *testing.T) {
	chunk := `let y=(0,g.createServerReference)("409b972cbb33d3e372f4ca9d3c8332677217064f62",g.callServer,void 0,g.findSourceMapURL,"fetchSubscriptionAction")`
	if id := actionIDFromChunk(chunk); id != "409b972cbb33d3e372f4ca9d3c8332677217064f62" {
		t.Fatalf("action id = %q", id)
	}
	if id := actionIDFromChunk("no action here"); id != "" {
		t.Fatalf("unexpected id %q", id)
	}
}
