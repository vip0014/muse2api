package hatch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	textSettle     = 2 * time.Second
	mediaSettle    = 4 * time.Second
	noMediaSettle  = 15 * time.Second
	pollInterval   = 500 * time.Millisecond
	chromeUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/156.0.0.0 Safari/537.36"
)

var capabilitiesList = []string{
	"chat_cancel",
	"delta_stream",
	"custom_reactions",
	"custom_reactions_facebook_thumbs_up_v1",
}

// MediaRef is one generated artifact exposed by the upstream turn.
type MediaRef struct {
	Kind  string // image | video
	Path  string // normalized sandbox path (no sandbox:// prefix)
	URL   string // optional signed URL
	Mime  string
	Label string
}

func (m MediaRef) identity() string {
	return m.Kind + "|" + m.Path + "|" + m.URL
}

var sandboxPattern = regexp.MustCompile(`sandbox://[^\s)"']+`)

// refusalMarkers is a compact subset of the upstream refusal phrases; the goal
// is early failure instead of waiting out the whole media budget.
var refusalMarkers = []string{
	"image tool is currently unavailable",
	"video tool is currently unavailable",
	"tool is currently unavailable",
	"no image was created",
	"no video was created",
	"could not generate",
	"couldn't generate",
	"cannot generate",
	"can't generate",
	"unable to generate",
	"failed to generate",
	"generation failed",
	"content policy",
	"safety guidelines",
	"against our policies",
	"not available in your",
	"not available for your account",
	"out of credits",
	"limit reached",
	"没生成成",
	"没生成成功",
	"未生成成功",
	"生成失败",
	"无法生成",
}

// Client owns one gateway connection plus chat session state. Turns are
// serialized because the upstream VM handles one conversation at a time, and
// every turn opens its own thread so concurrent API requests cannot interleave.
type Client struct {
	mu         sync.Mutex
	chatMu     sync.Mutex
	conn       *Conn
	session    *Session
	accountID  string
	site       *url.URL
	httpClient *http.Client
	nodeID     string
	registered bool

	// quotaActionID caches the Next.js server-action id that returns the
	// subscription snapshot. The id is build-specific, so it is re-discovered
	// whenever the upstream deployment changes.
	quotaMu       sync.Mutex
	quotaActionID string
	quotaActionAt time.Time

	// connKey fingerprints the session credentials the live connection was
	// built with. A different account (or re-imported cookies) must not reuse
	// the previous account's VM connection.
	connKey string
}

// NewClient builds a protocol client for one muse.ai site.
func NewClient(site *url.URL, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 25 * time.Second}
	}
	return &Client{site: site, httpClient: httpClient, nodeID: randomRequestID()}
}

func (c *Client) connect(ctx context.Context, credentials map[string]string) (*Conn, error) {
	key := credentialFingerprint(credentials)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil && !c.conn.IsClosed() && c.connKey == key {
		return c.conn, nil
	}
	if c.conn != nil {
		// Different credentials: the old connection belongs to another
		// account or a session that was replaced. Drop it before dialing.
		_ = c.conn.Close()
		c.conn = nil
		c.session = nil
		c.registered = false
		c.connKey = ""
	}
	session, err := Bootstrap(ctx, SessionOptions{
		BaseURL: c.site,
		Cookies: credentials,
		Client:  c.httpClient,
	})
	if err != nil {
		return nil, err
	}
	conn, err := Dial(ctx, session.WSURL)
	if err != nil {
		return nil, err
	}
	c.conn = conn
	c.session = session
	c.registered = false
	c.connKey = key
	return conn, nil
}

func (c *Client) forgetConnection() {
	c.mu.Lock()
	if c.conn != nil {
		_ = c.conn.Close()
	}
	c.conn = nil
	c.session = nil
	c.registered = false
	c.connKey = ""
	c.mu.Unlock()
}

func (c *Client) register(ctx context.Context, conn *Conn) error {
	c.mu.Lock()
	done := c.registered
	c.mu.Unlock()
	if done {
		return nil
	}
	_, _ = conn.Request(ctx, http.MethodPost, "/api/nodes/register", map[string]any{
		"node_id":      c.nodeID,
		"display_name": "Web",
		"platform":     "web",
		"commands_v2":  map[string]any{"ping": map[string]any{"description": "Connection liveness check"}},
	}, 15*time.Second)
	_, err := conn.Request(ctx, http.MethodPost, "/client/register-capabilities", map[string]any{
		"client_id":    c.nodeID,
		"platform":     "web",
		"display_name": "Muse Web",
		"version":      "0.0.0",
		"capabilities": map[string]any{
			"data_sources":     map[string]any{},
			"device_commands":  map[string]any{},
			"hatch_app_commands": map[string]any{},
			"rendering": map[string]any{
				"supported_presentations":          []string{"text_with_button", "generic_list"},
				"supported_inline_presentations":   []string{"option"},
				"supported_text_entities":          []string{},
			},
		},
	}, 15*time.Second)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.registered = true
	c.mu.Unlock()
	return nil
}

// ChatItem is one attachment or text entry in the outgoing chat turn, matching
// the web client's items[] shape.
type ChatItem struct {
	Type       string `json:"type"`
	Text       string `json:"text,omitempty"`
	MimeType   string `json:"mime_type,omitempty"`
	DataBase64 string `json:"data_base64,omitempty"`
	Filename   string `json:"filename,omitempty"`
}

// Chat sends one text-only prompt.
func (c *Client) Chat(ctx context.Context, credentials map[string]string, prompt string, wantMedia bool, timeout time.Duration) (string, []MediaRef, error) {
	return c.ChatWithItems(ctx, credentials, prompt, nil, wantMedia, timeout)
}

// ChatWithItems sends a prompt with optional inline image attachments
// (chat.stream items[]) and waits for the assistant reply plus media refs.
func (c *Client) ChatWithItems(ctx context.Context, credentials map[string]string, prompt string, mediaItems []ChatItem, wantMedia bool, timeout time.Duration) (string, []MediaRef, error) {
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	c.chatMu.Lock()
	defer c.chatMu.Unlock()
	sessionID := randomRequestID()
	deadline := time.Now().Add(timeout)
	conn, err := c.connect(ctx, credentials)
	if err != nil {
		return "", nil, err
	}
	if err := c.register(ctx, conn); err != nil {
		return "", nil, err
	}

	items := make([]any, 0, len(mediaItems)+1)
	for _, item := range mediaItems {
		items = append(items, item)
	}
	items = append(items, ChatItem{Type: "text", Text: prompt})
	body := map[string]any{
		"items":        items,
		"message":      prompt,
		"node_id":      c.nodeID,
		"capabilities": capabilitiesList,
		"session_id":   sessionID,
		"timezone":     "UTC",
		"metadata":     map[string]any{"thread_is_dictation_used": false},
	}
	messageID := ""
	if ack, err := conn.Request(ctx, http.MethodPost, "/chat/stream", body, 20*time.Second); err == nil {
		var payload map[string]any
		if ack.JSON(&payload) == nil {
			messageID = asString(payload["message_id"])
			if messageID == "" {
				messageID = asString(payload["id"])
			}
		}
	}

	var bestText string
	var bestMedia []MediaRef
	var lastText string
	var textStableAt time.Time
	mediaSignature := ""
	var mediaStableAt time.Time
	for {
		if err := ctx.Err(); err != nil {
			return bestText, bestMedia, err
		}
		if time.Now().After(deadline) {
			return bestText, bestMedia, fmt.Errorf("上游对话超时（最后回复：%s）", truncate([]byte(bestText), 160))
		}
		conn = c.currentConn()
		if conn == nil {
			if conn, err = c.connect(ctx, credentials); err != nil {
				sleepCtx(ctx, pollInterval)
				continue
			}
		}
		result, err := conn.Request(ctx, http.MethodGet, "/chat/history", map[string]any{
			"limit":      60,
			"session_id": sessionID,
		}, 20*time.Second)
		if err != nil {
			if conn.IsClosed() {
				c.forgetConnection()
			}
			sleepCtx(ctx, 800*time.Millisecond)
			continue
		}
		var payload map[string]any
		if err := result.JSON(&payload); err != nil {
			sleepCtx(ctx, pollInterval)
			continue
		}
		events := historyEvents(payload)
		userIndex := findUserIndex(events, messageID, prompt)
		if userIndex < 0 {
			sleepCtx(ctx, pollInterval)
			continue
		}
		after := events[userIndex+1:]
		currentText := latestAssistantText(after)
		var media []MediaRef
		if wantMedia {
			media = extractMedia(after)
		}
		if currentText != "" {
			bestText = currentText
		}
		if len(media) > 0 {
			bestMedia = media
		}

		// Refusal: the model answered with text and will not produce media.
		if len(media) == 0 && currentText != "" && isRefusal(currentText) {
			return bestText, nil, &MediaRefusalError{Message: truncate([]byte(currentText), 240)}
		}

		now := time.Now()
		if currentText != "" {
			if currentText == lastText {
				if textStableAt.IsZero() {
					textStableAt = now
				}
			} else {
				lastText = currentText
				textStableAt = now
			}
		}

		if wantMedia {
			if len(media) > 0 {
				sig := mediaSignatureOf(media)
				if sig != mediaSignature {
					mediaSignature = sig
					mediaStableAt = now
				}
				mediaStable := !mediaStableAt.IsZero() && now.Sub(mediaStableAt) >= mediaSettle
				textStable := !textStableAt.IsZero() && now.Sub(textStableAt) >= textSettle
				if mediaStable || textStable {
					return bestText, media, nil
				}
			} else {
				mediaSignature = ""
				mediaStableAt = time.Time{}
				if !textStableAt.IsZero() && now.Sub(textStableAt) >= noMediaSettle {
					return bestText, nil, &MediaRefusalError{Message: truncate([]byte(bestText), 240)}
				}
			}
		} else if !textStableAt.IsZero() && now.Sub(textStableAt) >= textSettle {
			return bestText, nil, nil
		}

		sleepCtx(ctx, pollInterval)
	}
}

func (c *Client) currentConn() *Conn {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn
}

// MediaRefusalError means the upstream turn produced text but no media.
type MediaRefusalError struct {
	Message string
}

func (e *MediaRefusalError) Error() string {
	return "模型未生成媒体，仅返回文本：" + e.Message
}

// Download fetches one media artifact over the gateway (or its signed URL).
func (c *Client) Download(ctx context.Context, credentials map[string]string, ref MediaRef) ([]byte, string, error) {
	conn, err := c.connect(ctx, credentials)
	if err != nil {
		return nil, "", err
	}
	if ref.Path != "" {
		clean := normalizeSandboxPath(ref.Path)
		for _, path := range []string{"/media/raw/" + clean, "/fs/raw/" + clean} {
			result, err := conn.Request(ctx, http.MethodGet, path, nil, 300*time.Second)
			if err == nil && result.Status >= 200 && result.Status < 300 && len(result.Body) > 0 {
				return result.Body, headerValue(result.Headers, "content-type"), nil
			}
			if c.currentConn() == nil {
				if conn, err = c.connect(ctx, credentials); err != nil {
					return nil, "", err
				}
			}
		}
	}
	if strings.HasPrefix(ref.URL, "http://") || strings.HasPrefix(ref.URL, "https://") {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, ref.URL, nil)
		if err != nil {
			return nil, "", err
		}
		request.Header.Set("User-Agent", chromeUserAgent)
		response, err := c.httpClient.Do(request)
		if err != nil {
			return nil, "", err
		}
		defer response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return nil, "", fmt.Errorf("媒体下载失败：HTTP %d", response.StatusCode)
		}
		data, err := io.ReadAll(io.LimitReader(response.Body, 512<<20))
		if err != nil {
			return nil, "", err
		}
		return data, response.Header.Get("Content-Type"), nil
	}
	return nil, "", errors.New("媒体缺少可下载的路径或 URL")
}

func headerValue(headers []Header, key string) string {
	for _, header := range headers {
		if strings.EqualFold(header.Key, key) {
			return header.Value
		}
	}
	return ""
}

func historyEvents(payload map[string]any) []map[string]any {
	node := payload
	if result, ok := payload["result"].(map[string]any); ok {
		node = result
	}
	for _, key := range []string{"chat_events", "events", "history"} {
		list, ok := node[key].([]any)
		if !ok {
			continue
		}
		out := make([]map[string]any, 0, len(list))
		for _, item := range list {
			if m, ok := item.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	}
	return nil
}

func eventName(event map[string]any) string {
	if name := asString(event["event_name"]); name != "" {
		return name
	}
	return asString(event["event"])
}

func eventText(event map[string]any) string {
	for _, key := range []string{"display_text", "text"} {
		if text := asString(event[key]); text != "" {
			return text
		}
	}
	if payload, ok := event["payload"].(map[string]any); ok {
		for _, key := range []string{"display_text", "text", "content", "delta"} {
			if text := asString(payload[key]); text != "" {
				return text
			}
		}
	}
	return ""
}

func findUserIndex(events []map[string]any, messageID, prompt string) int {
	if messageID != "" {
		for index, event := range events {
			if asString(event["message_id"]) == messageID {
				return index
			}
		}
	}
	needle := strings.TrimSpace(prompt)
	if len(needle) > 80 {
		needle = needle[:80]
	}
	for index := len(events) - 1; index >= 0; index-- {
		if eventName(events[index]) != "message.user" {
			continue
		}
		text := strings.TrimSpace(eventText(events[index]))
		if text != "" && (text == strings.TrimSpace(prompt) || strings.Contains(text, needle)) {
			return index
		}
	}
	return -1
}

func latestAssistantText(events []map[string]any) string {
	best := ""
	for _, event := range events {
		if eventName(event) != "message.assistant" {
			continue
		}
		if asString(event["reply_to_message_id"]) != "" {
			continue
		}
		if payload, ok := event["payload"].(map[string]any); ok {
			if asString(payload["reply_to_message_id"]) != "" {
				continue
			}
		}
		if text := strings.TrimSpace(eventText(event)); text != "" {
			best = text
		}
	}
	return best
}

func mediaSignatureOf(media []MediaRef) string {
	parts := make([]string, 0, len(media))
	for _, ref := range media {
		parts = append(parts, ref.identity())
	}
	return strings.Join(parts, "\n")
}

func isRefusal(text string) bool {
	lower := strings.ToLower(text)
	for _, marker := range refusalMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

var sandboxSuffixes = []struct {
	ext  string
	kind string
}{
	{".mp4", "video"}, {".webm", "video"}, {".mov", "video"}, {".m4v", "video"},
	{".png", "image"}, {".jpg", "image"}, {".jpeg", "image"}, {".webp", "image"}, {".gif", "image"},
}

func extractMedia(events []map[string]any) []MediaRef {
	found := map[string]MediaRef{}
	add := func(ref MediaRef) {
		if ref.Kind == "" || (ref.Path == "" && ref.URL == "") {
			return
		}
		if _, exists := found[ref.identity()]; !exists {
			found[ref.identity()] = ref
		}
	}
	var walk func(node any, kindHint string)
	walk = func(node any, kindHint string) {
		switch value := node.(type) {
		case map[string]any:
			if ref, ok := mediaRefFromNode(value, kindHint); ok {
				add(ref)
			}
			kind := kindHint
			if k := asString(value["kind"]); k == "image" || k == "video" {
				kind = k
			}
			for key, child := range value {
				switch key {
				case "path", "image_path", "video_path", "url", "variants", "mime",
					"mime_type", "mimeType", "label", "byte_len":
					continue
				}
				walk(child, kind)
			}
		case []any:
			for _, item := range value {
				walk(item, kindHint)
			}
		case string:
			for _, match := range sandboxPattern.FindAllString(value, -1) {
				walk(map[string]any{"path": match}, kindHint)
			}
		}
	}
	for _, event := range events {
		kindHint := ""
		if payload, ok := event["payload"].(map[string]any); ok {
			if k := asString(payload["kind"]); k == "image" || k == "video" {
				kindHint = k
			}
			walk(payload, kindHint)
		}
		if resources, ok := event["resources"]; ok && resources != nil {
			walk(resources, kindHint)
		}
	}
	out := make([]MediaRef, 0, len(found))
	for _, ref := range found {
		out = append(out, ref)
	}
	return out
}

func mediaRefFromNode(node map[string]any, kindHint string) (MediaRef, bool) {
	path := asString(node["path"])
	if path == "" {
		path = asString(node["image_path"])
	}
	if path == "" {
		path = asString(node["video_path"])
	}
	url := ""
	if variants, ok := node["variants"].(map[string]any); ok {
		url = asString(variants["original"])
	}
	if url == "" {
		url = asString(node["url"])
	}
	mime := asString(node["mime"])
	if mime == "" {
		mime = asString(node["mime_type"])
	}
	if mime == "" {
		mime = asString(node["mimeType"])
	}
	kind := asString(node["kind"])
	if kind != "image" && kind != "video" {
		kind = kindHint
	}
	if kind != "image" && kind != "video" {
		if strings.HasPrefix(mime, "video/") {
			kind = "video"
		} else if strings.HasPrefix(mime, "image/") {
			kind = "image"
		}
	}
	if kind != "image" && kind != "video" {
		lower := strings.ToLower(path)
		if lower == "" {
			lower = strings.ToLower(url)
		}
		for _, suffix := range sandboxSuffixes {
			if strings.HasSuffix(strings.SplitN(lower, "?", 2)[0], suffix.ext) {
				kind = suffix.kind
				break
			}
		}
	}
	if (kind != "image" && kind != "video") || (path == "" && url == "") {
		return MediaRef{}, false
	}
	ref := MediaRef{Kind: kind, Mime: mime, Label: asString(node["label"])}
	if path != "" {
		ref.Path = normalizeSandboxPath(path)
	}
	if url != "" {
		ref.URL = url
	}
	return ref, true
}

func normalizeSandboxPath(value string) string {
	text := strings.TrimSpace(value)
	text = strings.TrimPrefix(text, "sandbox://")
	text = strings.ReplaceAll(text, "\\", "/")
	for strings.Contains(text, "//") {
		text = strings.ReplaceAll(text, "//", "/")
	}
	return strings.TrimLeft(text, "/")
}

// ChatDelta is one incremental assistant text fragment.
type ChatDelta struct {
	Text string
	Done bool
	Err  error
}

// ChatStream sends a prompt on the account's main chat and streams assistant
// text deltas from chat.subscribe. The turn is serialized with all other chat
// work because the upstream VM handles one conversation at a time.
func (c *Client) ChatStream(ctx context.Context, credentials map[string]string, prompt string, timeout time.Duration) (<-chan ChatDelta, error) {
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	c.chatMu.Lock()
	conn, err := c.connect(ctx, credentials)
	if err != nil {
		c.chatMu.Unlock()
		return nil, err
	}
	if err := c.register(ctx, conn); err != nil {
		c.chatMu.Unlock()
		return nil, err
	}
	sub, err := conn.OpenStream(ctx, "/chat/subscribe", map[string]any{
		"after_stream_seq":     0,
		"after_chat_event_seq": 0,
		"capabilities":         capabilitiesList,
	})
	if err != nil {
		c.chatMu.Unlock()
		return nil, err
	}
	sleepCtx(ctx, 400*time.Millisecond)

	sendStart := time.Now().UnixMilli()
	body := map[string]any{
		"message":      prompt,
		"items":        []any{ChatItem{Type: "text", Text: prompt}},
		"node_id":      c.nodeID,
		"capabilities": capabilitiesList,
		"timezone":     "UTC",
	}
	if _, err := conn.Request(ctx, http.MethodPost, "/chat/stream", body, 20*time.Second); err != nil {
		sub.Close()
		c.chatMu.Unlock()
		return nil, err
	}

	out := make(chan ChatDelta, 64)
	go func() {
		defer close(out)
		defer func() {
			sub.Close()
			c.chatMu.Unlock()
		}()
		deadline := time.After(timeout)
		for {
			select {
			case <-ctx.Done():
				return
			case <-deadline:
				select {
				case out <- ChatDelta{Done: true}:
				default:
				}
				return
			case record, ok := <-sub.Events():
				if !ok {
					if err := sub.Err(); err != nil {
						select {
						case out <- ChatDelta{Err: err}:
						case <-ctx.Done():
						}
					} else {
						select {
						case out <- ChatDelta{Done: true}:
						case <-ctx.Done():
						}
					}
					return
				}
				if ts, ok := record["ts_ms"].(float64); ok && int64(ts) < sendStart {
					continue // replay of historical events
				}
				name := asString(record["event"])
				if name == "" {
					name = asString(record["event_name"])
				}
				payload, _ := record["payload"].(map[string]any)
				switch name {
				case "delta.text_append":
					text := asString(payload["text"])
					if text == "" {
						text = asString(payload["delta"])
					}
					if text != "" {
						select {
						case out <- ChatDelta{Text: text}:
						case <-ctx.Done():
							return
						}
					}
				case "delta.message_done", "message.assistant":
					select {
					case out <- ChatDelta{Done: true}:
					case <-ctx.Done():
					}
					return
				}
			}
		}
	}()
	return out, nil
}

func sleepCtx(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

