package hatch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"muse2api/internal/model"
	"muse2api/internal/muse/protocol"
)

// Usage and subscription state are exposed through a Next.js server action,
// not a REST endpoint. The action id is compiled into a lazily loaded chunk,
// so it changes with every deployment: the fallback below is tried first and a
// chunk scan rediscovers the id when the deployment rotates it.
const (
	quotaActionName     = "fetchSubscriptionAction"
	quotaFallbackAction = "409b972cbb33d3e372f4ca9d3c8332677217064f62"
	quotaPagePath       = "/settings"
	quotaActionTTL      = 12 * time.Hour
	quotaScanWorkers    = 12
	quotaScanLimit      = 512
	quotaChunkMaxBytes  = 2 << 20
	quotaBodyMaxBytes   = 4 << 20
)

// quotaPageCandidates are the authenticated surfaces that render the
// subscription snapshot. The post-update build moved the pane between routes,
// so discovery loads each page and scans the chunks they load. Server actions
// are app-global: the id found on any of them can be posted to the primary
// route.
var quotaPageCandidates = []string{quotaPagePath, "/subscriptions"}

var (
	actionHexRe = regexp.MustCompile(`[0-9a-f]{40,}`)
	// Next.js changed its app-router asset layout with the Turbopack build:
	// /_next/static/chunks/<name>.js is now
	// /_next/static/immutable/chunks/<name>.js, and chunk names are hashes.
	// Match both layouts and keep the whole path instead of a bare file name,
	// because each layout resolves to a different URL.
	scriptURLRe = regexp.MustCompile(`/_next/static/[^"'\s\\<>]+?\.js(?:\?[^"'\s\\<>]*)?`)
	chunkPathRe = regexp.MustCompile(`static/(?:immutable/)?chunks/[^"'\s\\<>]+?\.js`)
	// Server references are emitted as
	// (0,g.createServerReference)("<id>",g.callServer,void 0,...,"<name>").
	// The name is an argument, so read the call instead of scanning a fixed
	// byte window around the name.
	serverRefRe  = regexp.MustCompile(`createServerReference[^(]{0,40}\(([^)]{0,600})\)`)
	dplRe        = regexp.MustCompile(`dpl_[A-Za-z0-9]+`)
	loginMarkers = []string{"sign in", "log in", "login", "登录", "signin"}
)

// QueryQuota reads the current subscription snapshot for one account. It uses
// the same HTTP session (cookies) as the gateway bootstrap, but never wakes or
// contacts the VM.
func (c *Client) QueryQuota(ctx context.Context, creds protocol.AccountCredentials) (*model.Quota, error) {
	if c.site == nil {
		return nil, errors.New("额度查询缺少上游地址")
	}
	if len(creds.Cookies) == 0 {
		return nil, protocol.ErrSessionExpired
	}
	// The deadline covers queueing as well as the request itself, so an admin
	// refresh cannot wait out a slow background scan before its own work starts.
	ctx, cancel := context.WithTimeout(ctx, 50*time.Second)
	defer cancel()
	// A cold action-id cache can trigger a multi-chunk scan. Serialize it per
	// account instead of launching the same scan from auto-check and admin.
	select {
	case c.quotaGate <- struct{}{}:
		defer func() { <-c.quotaGate }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	actionID := c.cachedQuotaAction()
	subscription, err := c.callQuotaAction(ctx, creds.Cookies, actionID)
	if err == nil {
		c.rememberQuotaAction(actionID)
		return quotaFromSubscription(subscription)
	}
	if errors.Is(err, protocol.ErrSessionExpired) {
		return nil, err
	}

	// The cached/fallback id was rejected (new deployment). Rebuild it from
	// the chunks the settings page loads and retry once.
	discovered, discoverErr := c.discoverQuotaAction(ctx, creds.Cookies)
	if discoverErr != nil {
		if errors.Is(discoverErr, protocol.ErrSessionExpired) {
			return nil, discoverErr
		}
		return nil, fmt.Errorf("额度接口定位失败：%v（首次原因：%v）", discoverErr, err)
	}
	subscription, err = c.callQuotaAction(ctx, creds.Cookies, discovered)
	if err != nil {
		return nil, err
	}
	c.rememberQuotaAction(discovered)
	return quotaFromSubscription(subscription)
}

func (c *Client) cachedQuotaAction() string {
	c.quotaMu.Lock()
	defer c.quotaMu.Unlock()
	if c.quotaActionID != "" && time.Since(c.quotaActionAt) < quotaActionTTL {
		return c.quotaActionID
	}
	return quotaFallbackAction
}

func (c *Client) rememberQuotaAction(id string) {
	if id == "" {
		return
	}
	c.quotaMu.Lock()
	c.quotaActionID = id
	c.quotaActionAt = time.Now()
	c.quotaMu.Unlock()
}

// callQuotaAction invokes fetchSubscriptionAction and parses the Flight reply.
func (c *Client) callQuotaAction(ctx context.Context, cookies map[string]string, actionID string) (map[string]any, error) {
	if actionID == "" {
		return nil, errors.New("空的额度接口标识")
	}
	base := strings.TrimRight(c.site.String(), "/")
	endpoint := base + quotaPagePath
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader("[]"))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/x-component")
	req.Header.Set("Content-Type", "text/plain;charset=UTF-8")
	req.Header.Set("Next-Action", actionID)
	req.Header.Set("Cookie", cookieHeader(cookies))
	req.Header.Set("Origin", c.site.Scheme+"://"+c.site.Host)
	req.Header.Set("Referer", endpoint)
	req.Header.Set("User-Agent", chromeUserAgent)
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Dest", "empty")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, quotaBodyMaxBytes))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, protocol.ErrSessionExpired
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("额度接口 HTTP %d: %s", resp.StatusCode, truncate(body, 160))
	}
	contentType := resp.Header.Get("Content-Type")
	if !strings.Contains(contentType, "text/x-component") {
		if looksLikeLoginPage(body) {
			return nil, protocol.ErrSessionExpired
		}
		return nil, fmt.Errorf("额度接口返回了 %s，接口标识可能已过期", contentType)
	}
	return parseFlightSubscription(body)
}

// parseFlightSubscription extracts the subscription object from a React
// Flight stream: rows are "<id>:<json>", one per line.
func parseFlightSubscription(body []byte) (map[string]any, error) {
	for _, line := range bytes.Split(body, []byte("\n")) {
		line = bytes.TrimSpace(line)
		idx := bytes.IndexByte(line, ':')
		if idx <= 0 || idx == len(line)-1 {
			continue
		}
		var payload map[string]any
		if json.Unmarshal(line[idx+1:], &payload) != nil {
			continue
		}
		if success, ok := payload["success"].(bool); ok && !success {
			message := asString(payload["message"])
			if message == "" {
				message = "上游返回 success=false"
			}
			return nil, errors.New(message)
		}
		if subscription, ok := payload["subscription"].(map[string]any); ok {
			return subscription, nil
		}
	}
	return nil, errors.New("额度响应中没有 subscription 数据")
}

func quotaFromSubscription(subscription map[string]any) (*model.Quota, error) {
	tier, _ := subscription["tier"].(map[string]any)
	usage, _ := subscription["usage"].(map[string]any)
	quota := &model.Quota{Found: true, CheckedAt: time.Now().Unix()}
	quota.Plan = asString(tier["name"])
	if quota.Plan == "" {
		quota.Plan = asString(tier["tierId"])
	}
	quota.WeeklyReset = asString(subscription["statusSubtitle"])
	quota.WeeklyUsedPct = asFloat(usage["percentUsed"])
	if total := asFloat(subscription["topupTotal"]); total > 0 {
		balance := asFloat(subscription["topupBalance"])
		used := (total - balance) / total * 100
		used = clampPercent(used)
		quota.ExtraUsedPct = &used
	}
	quota.ExtraLeft = asString(subscription["topupRowValueLabel"])
	if quota.ExtraLeft != "" {
		quota.ExtraExpires = "从不过期"
	}
	if quota.Plan == "" && quota.WeeklyReset == "" && quota.ExtraLeft == "" {
		return nil, errors.New("额度响应缺少可识别字段")
	}
	return quota, nil
}

func clampPercent(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

func asFloat(value any) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case json.Number:
		f, _ := v.Float64()
		return f
	case string:
		var f float64
		if _, err := fmt.Sscanf(strings.TrimSpace(v), "%f", &f); err == nil {
			return f
		}
	}
	return 0
}

func looksLikeLoginPage(body []byte) bool {
	head := body
	if len(head) > 8192 {
		head = head[:8192]
	}
	lower := strings.ToLower(string(head))
	for _, marker := range loginMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// discoverQuotaAction walks the authenticated pages' client chunk graphs until
// it finds the server-action reference. A page only lists the chunks it needs
// for the first render; the action id usually lives one or two hops deeper, so
// chunk references are followed breadth-first.
func (c *Client) discoverQuotaAction(ctx context.Context, cookies map[string]string) (string, error) {
	base := strings.TrimRight(c.site.String(), "/")
	queue := make([]string, 0, 64)
	seen := make(map[string]bool)
	dpl := ""
	var lastErr error
	for _, path := range quotaPageCandidates {
		html, err := c.fetchText(ctx, cookies, base+path, quotaBodyMaxBytes)
		if err != nil {
			if errors.Is(err, protocol.ErrSessionExpired) {
				return "", err
			}
			lastErr = err
			continue
		}
		// RSC flight payloads embed asset URLs JSON-escaped (\/_next\/static\/...).
		// Normalize them so both the document head and the stream are scanned.
		html = strings.ReplaceAll(html, `\/`, "/")
		html = strings.ReplaceAll(html, "&amp;", "&")
		if dpl == "" {
			dpl = dplRe.FindString(html)
		}
		// Some deployments inline the client reference into the flight payload
		// instead of a separate chunk; try the page text first.
		if id := actionIDFromChunk(html); id != "" {
			return id, nil
		}
		for _, path := range scriptURLRe.FindAllString(html, -1) {
			url := path
			if !strings.HasPrefix(url, "http") {
				url = base + url
			}
			if seen[url] {
				continue
			}
			seen[url] = true
			queue = append(queue, url)
		}
	}
	if len(queue) == 0 {
		if lastErr != nil {
			return "", lastErr
		}
		return "", errors.New("设置页没有引用任何脚本")
	}
	return c.scanChunkGraph(ctx, cookies, base, dpl, queue, seen)
}

// scanChunkGraph downloads chunks in small parallel batches and follows every
// chunk reference they contain. Each batch is bounded, so cancelling a caller
// waits at most one batch instead of every queued download.
func (c *Client) scanChunkGraph(ctx context.Context, cookies map[string]string, base, dpl string, queue []string, seen map[string]bool) (string, error) {
	scanned := 0
	serverRefs := 0
	var renamedID string
	renamed := make(map[string]struct{})
	refIDs := make(map[string]struct{})

	for len(queue) > 0 && scanned < quotaScanLimit {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		batchSize := quotaScanWorkers
		if len(queue) < batchSize {
			batchSize = len(queue)
		}
		batch := queue[:batchSize]
		queue = queue[batchSize:]

		results := make(chan string, len(batch))
		var wg sync.WaitGroup
		for _, url := range batch {
			wg.Add(1)
			go func(url string) {
				defer wg.Done()
				text, err := c.fetchText(ctx, cookies, url, quotaChunkMaxBytes)
				if err != nil {
					return
				}
				results <- text
			}(url)
		}
		wg.Wait()
		close(results)

		for text := range results {
			scanned++
			if id := actionIDFromChunk(text); id != "" {
				return id, nil
			}
			// A renamed action still mentions "subscription" in its reference
			// arguments. Keep it only when it is the single such reference.
			if id := subscriptionActionFromChunk(text); id != "" {
				renamed[id] = struct{}{}
				renamedID = id
			}
			for _, call := range serverRefRe.FindAllStringSubmatch(text, -1) {
				serverRefs++
				if id := actionHexRe.FindString(call[1]); id != "" {
					refIDs[id] = struct{}{}
				}
			}
			for _, path := range chunkPathRe.FindAllString(text, -1) {
				url := base + "/_next/" + path
				if dpl != "" {
					url += "?dpl=" + dpl
				}
				if seen[url] {
					continue
				}
				seen[url] = true
				queue = append(queue, url)
			}
		}
	}
	if len(renamed) == 1 {
		return renamedID, nil
	}
	// Last resort for builds that drop the reference name entirely: a single
	// server action on the settings page can only be the quota fetch. The
	// caller validates it by requiring a subscription payload.
	if len(refIDs) == 1 {
		for id := range refIDs {
			return id, nil
		}
	}
	return "", fmt.Errorf("部署的脚本中没有找到额度接口标识（已扫描 %d 个脚本，命中 %d 个 server action 引用）", scanned, serverRefs)
}

// actionIDFromChunk returns the id of the server reference whose argument list
// names the quota action.
func actionIDFromChunk(text string) string {
	for _, call := range serverRefRe.FindAllStringSubmatch(text, -1) {
		args := call[1]
		if !strings.Contains(args, quotaActionName) {
			continue
		}
		if id := actionHexRe.FindString(args); id != "" {
			return id
		}
	}
	// Fallback for builds that emit the call differently: the id still sits
	// just before the action name.
	idx := strings.Index(text, quotaActionName)
	if idx < 0 {
		return ""
	}
	start := idx - 600
	if start < 0 {
		start = 0
	}
	end := idx + len(quotaActionName) + 120
	if end > len(text) {
		end = len(text)
	}
	return actionHexRe.FindString(text[start:end])
}

// subscriptionActionFromChunk finds a server reference whose arguments still
// describe a subscription action after a rename, e.g. getSubscriptionAction.
func subscriptionActionFromChunk(text string) string {
	for _, call := range serverRefRe.FindAllStringSubmatch(text, -1) {
		args := strings.ToLower(call[1])
		if !strings.Contains(args, "subscription") && !strings.Contains(args, "quota") {
			continue
		}
		if id := actionHexRe.FindString(call[1]); id != "" {
			return id
		}
	}
	return ""
}

func (c *Client) fetchText(ctx context.Context, cookies map[string]string, rawURL string, limit int64) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Cookie", cookieHeader(cookies))
	req.Header.Set("User-Agent", chromeUserAgent)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return "", protocol.ErrSessionExpired
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return "", err
	}
	return string(data), nil
}
