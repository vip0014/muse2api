package hatch

import (
	"context"
	"errors"
	"net/url"
	"testing"
	"time"

	"muse2api/internal/muse/protocol"
)

func TestQueryQuotaWaitIsCancellable(t *testing.T) {
	site, err := url.Parse("https://example.invalid")
	if err != nil {
		t.Fatalf("parse site: %v", err)
	}
	client := NewClientWithWSURL(site, nil, "")
	client.quotaGate <- struct{}{}
	defer func() { <-client.quotaGate }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	_, err = client.QueryQuota(ctx, protocol.AccountCredentials{ID: "acc_1", Cookies: map[string]string{"hatch_sess": "x"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("QueryQuota error = %v, want context canceled", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("quota gate wait took %s", elapsed)
	}
}
