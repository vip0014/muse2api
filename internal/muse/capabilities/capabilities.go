package capabilities

import "time"

type State string

const (
	Implemented State = "IMPLEMENTED"
	Verified    State = "VERIFIED"
	Partial     State = "PARTIAL"
	Unverified  State = "UNVERIFIED"
	Unavailable State = "UNAVAILABLE"
)

type Capability struct {
	ID            string   `json:"id"`
	Alias         string   `json:"alias"`
	UpstreamModel string   `json:"upstream_model"`
	Features      []string `json:"features"`
	Status        State    `json:"status"`
	LastVerified  int64    `json:"last_verified,omitempty"`
	Reason        string   `json:"reason,omitempty"`
}

// protocolVerifiedAt is the 2026-10-31 pure-protocol acceptance run
// (Noise XX handshake + chat.stream/history image and video generation).
var protocolVerifiedAt = time.Date(2026, 10, 31, 15, 30, 0, 0, time.UTC).Unix()

func Protocol() []Capability {
	return []Capability{
		{ID: "muse-spark", Alias: "muse-spark", UpstreamModel: "muse.ai hatch gateway", Features: []string{"chat", "responses", "sse"}, Status: Verified, LastVerified: protocolVerifiedAt, Reason: "2026-10-31 纯协议（Noise XX + protobuf）对话往返验收通过"},
		{ID: "muse-image", Alias: "muse-image", UpstreamModel: "muse.ai hatch gateway", Features: []string{"generation", "async"}, Status: Verified, LastVerified: protocolVerifiedAt, Reason: "2026-10-31 chat.stream + chat.history 纯协议文生图验收通过（webp 落盘）"},
		{ID: "muse-video", Alias: "muse-video", UpstreamModel: "muse.ai hatch gateway", Features: []string{"generation", "async"}, Status: Verified, LastVerified: protocolVerifiedAt, Reason: "2026-10-31 chat.stream + chat.history 纯协议视频生成验收通过（MP4 落盘）"},
		{ID: "muse-session", Alias: "session", UpstreamModel: "muse.ai/api/session", Features: []string{"validate", "renew", "cookie_sync", "vm_wake"}, Status: Verified, LastVerified: protocolVerifiedAt, Reason: "HTTP Session/VM 唤醒链路已验证"},
	}
}

