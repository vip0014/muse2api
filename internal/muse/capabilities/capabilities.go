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

// realVerifiedAt is the 2026-10-26 real Free-plan account acceptance run.
var realVerifiedAt = time.Date(2026, 10, 26, 15, 54, 38, 0, time.UTC).Unix()

type Capability struct {
	ID            string   `json:"id"`
	Alias         string   `json:"alias"`
	UpstreamModel string   `json:"upstream_model"`
	Features      []string `json:"features"`
	Status        State    `json:"status"`
	LastVerified  int64    `json:"last_verified,omitempty"`
	Reason        string   `json:"reason,omitempty"`
}

func Current() []Capability {
	return []Capability{
		{ID: "muse-spark", Alias: "muse-spark", UpstreamModel: "unverified-web-agent", Features: []string{"chat", "responses", "sse"}, Status: Unavailable, Reason: "缺少真实上游对话 HTTP/WebSocket 报文；旧实现依赖 Chromium/CDP"},
		{ID: "muse-image", Alias: "muse-image", UpstreamModel: "unverified-web-agent", Features: []string{"generation", "edit", "async"}, Status: Unavailable, Reason: "缺少真实上游生图协议；不伪造生成结果"},
		{ID: "muse-video", Alias: "muse-video", UpstreamModel: "unverified-web-agent", Features: []string{"generation", "reference_image", "async"}, Status: Unavailable, Reason: "缺少真实上游视频任务协议；不伪造任务成功"},
		{ID: "muse-session", Alias: "session", UpstreamModel: "muse.ai/api/session", Features: []string{"validate", "renew", "cookie_sync"}, Status: Implemented, Reason: "依据旧版真实 HTTP 代码实现；真实账号集成仍需合法凭据"},
	}
}

func Browser() []Capability {
	return []Capability{
		{ID: "muse-spark", Alias: "muse-spark", UpstreamModel: "muse.ai web agent", Features: []string{"chat", "responses", "sse"}, Status: Verified, LastVerified: realVerifiedAt, Reason: "2026-10-26 真实 Free 账号完成对话与 Responses 文本验收"},
		{ID: "muse-image", Alias: "muse-image", UpstreamModel: "muse.ai web agent", Features: []string{"generation", "edit", "async", "reference_image"}, Status: Verified, LastVerified: realVerifiedAt, Reason: "2026-10-26 真实账号文生图与参考图编辑验收通过"},
		{ID: "muse-video", Alias: "muse-video", UpstreamModel: "muse.ai web agent", Features: []string{"generation", "reference_image", "async"}, Status: Verified, LastVerified: realVerifiedAt, Reason: "2026-10-26 真实账号视频生成验收通过（MP4 1280x720）"},
		{ID: "muse-session", Alias: "session", UpstreamModel: "muse.ai/api/session", Features: []string{"validate", "renew", "cookie_sync", "vm_wake", "quota"}, Status: Verified, LastVerified: realVerifiedAt, Reason: "2026-10-26 真实 Session/VM 唤醒/额度读取验收通过"},
	}
}
