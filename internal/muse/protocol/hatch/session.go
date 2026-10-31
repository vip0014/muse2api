package hatch

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Gateway constants come from the web bundle (protocol.json).
const (
	DefaultLBHost = "hatch.metaaivm.com"
	DefaultWSPath = "/v1/noise"
	DefaultAppID  = "hatch-web"
)

// Session is a bootstrapped gateway admission (VM + auth token).
type Session struct {
	VMID      string
	Endpoint  string
	AuthToken string
	Notary    string
	WSURL     string
}

// SessionOptions carries the HTTP bootstrap inputs.
type SessionOptions struct {
	BaseURL *url.URL // e.g. https://muse.ai
	Client  *http.Client
	Cookies map[string]string
	LBHost  string
	WSPath  string
	AppID   string
}

// Bootstrap resolves the account VM, wakes it when needed, obtains the hatch
// auth token (and optional noise notary token) and builds the wss URL.
func Bootstrap(ctx context.Context, opts SessionOptions) (*Session, error) {
	if opts.BaseURL == nil {
		return nil, errors.New("missing base url")
	}
	if opts.Client == nil {
		opts.Client = &http.Client{Timeout: 25 * time.Second}
	}
	if opts.Cookies == nil {
		opts.Cookies = map[string]string{}
	}
	lbHost := opts.LBHost
	if lbHost == "" {
		lbHost = DefaultLBHost
	}
	wsPath := opts.WSPath
	if wsPath == "" {
		wsPath = DefaultWSPath
	}
	appID := opts.AppID
	if appID == "" {
		appID = DefaultAppID
	}

	info, err := opts.requestJSON(ctx, http.MethodGet, "/api/session?vm_state=live", nil)
	if err != nil {
		return nil, err
	}
	vmID := asString(info["vm_id"])
	endpoint := asString(info["endpoint_url"])
	state := asString(info["vm_state"])
	if vmID == "" || endpoint == "" {
		return nil, fmt.Errorf("muse.ai did not assign a VM (status=%v)", info["status"])
	}

	if !strings.EqualFold(state, "RUNNING") {
		if err := opts.wakeVM(ctx, vmID); err != nil {
			return nil, err
		}
		deadline := time.Now().Add(45 * time.Second)
		for {
			info, err = opts.requestJSON(ctx, http.MethodGet, "/api/session?vm_state=live", nil)
			if err == nil {
				if id := asString(info["vm_id"]); id != "" {
					vmID = id
				}
				if ep := asString(info["endpoint_url"]); ep != "" {
					endpoint = ep
				}
				state = asString(info["vm_state"])
				if strings.EqualFold(state, "RUNNING") {
					break
				}
			}
			if time.Now().After(deadline) {
				return nil, fmt.Errorf("VM %s did not become RUNNING (state=%s)", vmID, state)
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(1500 * time.Millisecond):
			}
		}
		// Let the VM's noise listener bind after a wake.
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(1200 * time.Millisecond):
		}
	}

	tokenResp, err := opts.requestJSON(ctx, http.MethodPost, "/api/hatch/token", map[string]any{
		"vmAddress": endpoint,
		"vmName":    vmID,
	})
	if err != nil {
		var authErr *AuthError
		if errors.As(err, &authErr) {
			// A stale hatch_vml cookie is the usual cause of 401/403 here;
			// refresh /api/session once and retry the token request.
			if _, refreshErr := opts.requestJSON(ctx, http.MethodGet, "/api/session?vm_state=live", nil); refreshErr == nil {
				tokenResp, err = opts.requestJSON(ctx, http.MethodPost, "/api/hatch/token", map[string]any{
					"vmAddress": endpoint,
					"vmName":    vmID,
				})
			}
		}
	}
	if err != nil {
		return nil, err
	}
	authToken := strings.TrimSpace(asString(tokenResp["token"]))
	if authToken == "" {
		return nil, errors.New("hatch/token response is missing token")
	}

	notary := ""
	if nr, err := opts.requestJSON(ctx, http.MethodPost, "/api/hatch/noise-notary-token", map[string]any{"vmName": vmID}); err == nil {
		notary = strings.TrimSpace(asString(nr["notaryToken"]))
	}

	params := url.Values{}
	params.Set("vm_id", vmID)
	params.Set("auth_token", authToken)
	params.Set("app_id", appID)
	params.Set("request_id", randomRequestID())
	if notary != "" {
		params.Set("notary_token", notary)
	}
	wsURL := "wss://" + lbHost + wsPath + "?" + params.Encode()
	return &Session{VMID: vmID, Endpoint: endpoint, AuthToken: authToken, Notary: notary, WSURL: wsURL}, nil
}

func (o *SessionOptions) requestJSON(ctx context.Context, method, path string, body any) (map[string]any, error) {
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(buf)
	}
	endpoint := strings.TrimRight(o.BaseURL.String(), "/") + path
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return nil, err
	}
	// Application headers copied from the web client's captured requests; the
	// reference implementation relies on the same set.
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9,en-GB;q=0.8")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Cookie", cookieHeader(o.Cookies))
	origin := o.BaseURL.Scheme + "://" + o.BaseURL.Host
	req.Header.Set("Origin", origin)
	req.Header.Set("Referer", origin+"/")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Priority", "u=4")
	req.Header.Set("Pragma", "no-cache")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/156.0.0.0 Safari/537.36")

	resp, err := o.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	for _, cookie := range resp.Cookies() {
		if cookie.Name != "" && cookie.Value != "" {
			o.Cookies[cookie.Name] = cookie.Value
		}
	}
	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, &AuthError{Status: resp.StatusCode, Body: truncate(payload, 180)}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s %s: upstream HTTP %d %s", method, path, resp.StatusCode, truncate(payload, 180))
	}
	var out map[string]any
	if err := json.Unmarshal(payload, &out); err != nil {
		return nil, fmt.Errorf("%s %s: invalid JSON: %w", method, path, err)
	}
	return out, nil
}

func (o *SessionOptions) wakeVM(ctx context.Context, vmID string) error {
	attempt := make([]byte, 16)
	if _, err := rand.Read(attempt); err != nil {
		return err
	}
	_, err := o.requestJSON(ctx, http.MethodPost, "/api/hatch/vm/wake", map[string]any{
		"vm_id":              vmID,
		"retry_count":        0,
		"connect_attempt_id": hex.EncodeToString(attempt),
	})
	return err
}

func cookieHeader(cookies map[string]string) string {
	keys := make([]string, 0, len(cookies))
	for key, value := range cookies {
		if value != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+cookies[key])
	}
	return strings.Join(parts, "; ")
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

// AuthError marks an upstream 401/403; the bootstrap retries once after
// refreshing the session because a stale hatch_vml cookie is the usual cause.
type AuthError struct {
	Status int
	Body   string
}

func (e *AuthError) Error() string {
	return fmt.Sprintf("upstream auth error %d: %s", e.Status, e.Body)
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}
