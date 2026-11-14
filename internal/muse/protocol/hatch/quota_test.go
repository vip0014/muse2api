package hatch

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"muse2api/internal/muse/protocol"
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
	renamed := `(0,g.createServerReference)("0123456789abcdef0123456789abcdef01234567",g.callServer,void 0,g.findSourceMapURL,"getSubscriptionAction")`
	if id := subscriptionActionFromChunk(renamed); id != "0123456789abcdef0123456789abcdef01234567" {
		t.Fatalf("renamed action id = %q", id)
	}
}

func TestChunkPatternsCoverBothBundlers(t *testing.T) {
	paths := []string{
		`/_next/static/immutable/chunks/04d5giu9rtvpf.js`,
		`/_next/static/chunks/app/(main)/settings/page-abc123.js`,
		`/_next/static/immutable/chunks/2x30p-winv-at.js?dpl=dpl_abc`,
	}
	for _, path := range paths {
		if got := scriptURLRe.FindString(path); got != path {
			t.Fatalf("scriptURLRe(%q) = %q", path, got)
		}
	}
	lazy := `e.u("static/immutable/chunks/1y-g2x5eo-k-y.js")`
	if got := chunkPathRe.FindString(lazy); got != "static/immutable/chunks/1y-g2x5eo-k-y.js" {
		t.Fatalf("chunkPathRe = %q", got)
	}
	if got := chunkPathRe.FindString(`e.u("static/chunks/4592.8f1c.js")`); got != "static/chunks/4592.8f1c.js" {
		t.Fatalf("webpack chunkPathRe = %q", got)
	}
}

const quotaActionID = "0123456789abcdef0123456789abcdef01234567"

// turbopackSite reproduces the post-update deployment shape: chunks live under
// /_next/static/immutable/chunks/, the action id sits two hops away from the
// page HTML, and the previous deployment's id is answered with 409.
func turbopackSite(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/settings", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			if r.Header.Get("Next-Action") != quotaActionID {
				w.Header().Set("Content-Type", "text/plain;charset=UTF-8")
				w.WriteHeader(http.StatusConflict)
				io.WriteString(w, "Server Action unavailable.")
				return
			}
			w.Header().Set("Content-Type", "text/x-component")
			io.WriteString(w, quotaFlightSample)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!DOCTYPE html><html><head>`+
			`<script src="/_next/static/immutable/chunks/first.js" async=""></script>`+
			`</head><body><script>self.__next_f.push([1,"\/_next\/static\/immutable\/chunks\/first.js"])</script></body></html>`)
	})
	mux.HandleFunc("/_next/static/immutable/chunks/first.js", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `(globalThis.TURBOPACK=[]).push([1,e=>{e.u("static/immutable/chunks/second.js")}])`)
	})
	mux.HandleFunc("/_next/static/immutable/chunks/second.js", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `let y=(0,g.createServerReference)("`+quotaActionID+
			`",g.callServer,void 0,g.findSourceMapURL,"fetchSubscriptionAction")`)
	})
	return httptest.NewServer(mux)
}

func TestDiscoverQuotaActionTurbopackChunkGraph(t *testing.T) {
	srv := turbopackSite(t)
	defer srv.Close()
	site, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	client := NewClientWithWSURL(site, srv.Client(), "")
	id, err := client.discoverQuotaAction(context.Background(), map[string]string{"hatch_sess": "x"})
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if id != quotaActionID {
		t.Fatalf("action id = %q", id)
	}
}

func TestQueryQuotaRediscoversRotatedAction(t *testing.T) {
	srv := turbopackSite(t)
	defer srv.Close()
	site, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	client := NewClientWithWSURL(site, srv.Client(), "")
	quota, err := client.QueryQuota(context.Background(), protocolCredentials())
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if quota.Plan != "Muse 免费版" || quota.WeeklyUsedPct != 16 {
		t.Fatalf("unexpected quota: %+v", *quota)
	}
}

func protocolCredentials() protocol.AccountCredentials {
	return protocol.AccountCredentials{ID: "acc_1", Cookies: map[string]string{"hatch_sess": "x"}}
}
