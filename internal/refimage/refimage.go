// Package refimage normalizes user-supplied reference images before they reach
// the browser. The API layer can therefore reject bad input with a real 400
// instead of creating a task that only fails later in the worker.
package refimage

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	MaxImages        = 4
	MaxImageBytes    = 10 << 20
	MaxTotalBytes    = 24 << 20
	httpTimeout      = 20 * time.Second
	maxRedirects     = 10
	defaultImageMIME = "image/png"
)

var (
	ErrTooManyImages     = errors.New("参考图最多 4 张")
	ErrImageTooLarge     = errors.New("单张参考图超过 10 MiB")
	ErrTotalTooLarge     = errors.New("参考图总大小超过 24 MiB")
	ErrNotImage          = errors.New("参考图不是图片")
	ErrLocalFileDisabled = errors.New("参考图本地路径被拒绝：MUSE2API_REF_IMAGE_LOCAL_FILES=0")
	ErrPrivateNetwork    = errors.New("参考图地址被拒绝：仅允许公网地址")
	ErrInvalidReference  = errors.New("参考图格式无效")
)

// Policy controls where reference images may come from. Both switches are off
// by default; the API exposes them only for trusted single-user deployments.
type Policy struct {
	AllowLocalFiles     bool
	AllowPrivateNetwork bool
}

// Normalized is one validated reference image.
type Normalized struct {
	Data []byte
	MIME string
}

// DataURL returns the canonical form used by the browser attachment layer.
func (n Normalized) DataURL() string {
	return "data:" + n.MIME + ";base64," + base64.StdEncoding.EncodeToString(n.Data)
}

// Normalize validates one reference. It accepts data URLs, bare base64,
// http(s) URLs, and (only when enabled) local file paths.
func Normalize(ctx context.Context, raw string, policy Policy) (Normalized, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Normalized{}, fmt.Errorf("%w: 内容为空", ErrInvalidReference)
	}
	if strings.HasPrefix(raw, "data:") {
		data, mimeType, err := DecodeDataURL(raw)
		if err != nil {
			return Normalized{}, err
		}
		return checked(data, mimeType)
	}
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		data, mimeType, err := fetchHTTP(ctx, raw, policy)
		if err != nil {
			return Normalized{}, err
		}
		return checked(data, mimeType)
	}
	if isLikelyPath(raw) {
		if !policy.AllowLocalFiles {
			return Normalized{}, ErrLocalFileDisabled
		}
		return readLocalFile(raw)
	}
	data, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return Normalized{}, fmt.Errorf("%w: 必须是 URL、Data URL、本地图片或有效 Base64", ErrInvalidReference)
	}
	return checked(data, defaultImageMIME)
}

// NormalizeAll validates an ordered list of references and returns canonical
// data URLs. It enforces the per-image and aggregate limits before any task is
// created.
func NormalizeAll(ctx context.Context, raws []string, policy Policy) ([]string, error) {
	if len(raws) > MaxImages {
		return nil, ErrTooManyImages
	}
	out := make([]string, 0, len(raws))
	total := 0
	for index, raw := range raws {
		normalized, err := Normalize(ctx, raw, policy)
		if err != nil {
			return nil, fmt.Errorf("第 %d 张参考图：%w", index+1, err)
		}
		total += len(normalized.Data)
		if total > MaxTotalBytes {
			return nil, ErrTotalTooLarge
		}
		out = append(out, normalized.DataURL())
	}
	return out, nil
}

// DecodeDataURL decodes a data URL produced by Normalize (or supplied by the
// caller) and validates its embedded MIME and size.
func DecodeDataURL(raw string) ([]byte, string, error) {
	head, payload, ok := strings.Cut(raw, ",")
	if !ok || !strings.HasPrefix(strings.ToLower(head), "data:") {
		return nil, "", fmt.Errorf("%w: Data URL 格式无效", ErrInvalidReference)
	}
	meta := head[len("data:"):]
	parts := strings.Split(meta, ";")
	mimeType := defaultImageMIME
	if parts[0] != "" {
		mimeType = parts[0]
	}
	isBase64 := false
	for _, part := range parts[1:] {
		if strings.EqualFold(strings.TrimSpace(part), "base64") {
			isBase64 = true
			break
		}
	}
	var data []byte
	var err error
	if isBase64 {
		data, err = base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(payload))
	} else {
		var decoded string
		decoded, err = url.PathUnescape(payload)
		data = []byte(decoded)
	}
	if err != nil {
		return nil, "", fmt.Errorf("%w: Data URL 编码无效", ErrInvalidReference)
	}
	normalized, err := checked(data, mimeType)
	if err != nil {
		return nil, "", err
	}
	return normalized.Data, normalized.MIME, nil
}

func checked(data []byte, mimeType string) (Normalized, error) {
	if len(data) == 0 {
		return Normalized{}, fmt.Errorf("%w: 内容为空", ErrInvalidReference)
	}
	if len(data) > MaxImageBytes {
		return Normalized{}, ErrImageTooLarge
	}
	mimeType, err := normalizeMIME(mimeType)
	if err != nil {
		return Normalized{}, err
	}
	return Normalized{Data: data, MIME: mimeType}, nil
}

func normalizeMIME(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = defaultImageMIME
	}
	mediaType, _, err := mime.ParseMediaType(raw)
	if err != nil || mediaType == "" {
		return "", fmt.Errorf("%w: MIME 无效", ErrInvalidReference)
	}
	mediaType = strings.ToLower(mediaType)
	if !strings.HasPrefix(mediaType, "image/") {
		return "", fmt.Errorf("%w: %s", ErrNotImage, mediaType)
	}
	return mediaType, nil
}

func isLikelyPath(raw string) bool {
	if strings.HasPrefix(raw, "file://") {
		return true
	}
	// Base64 uses '/' and '+', so separators alone cannot identify a path.
	// Only an existing regular file is treated as a local reference; long
	// payloads are never probed as filenames.
	if len(raw) <= 4096 {
		if info, err := os.Stat(raw); err == nil && info.Mode().IsRegular() {
			return true
		}
	}
	return false
}

func readLocalFile(raw string) (Normalized, error) {
	path := raw
	if strings.HasPrefix(raw, "file://") {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Path == "" {
			return Normalized{}, fmt.Errorf("%w: 本地文件 URL 无效", ErrInvalidReference)
		}
		path = parsed.Path
		if len(path) >= 3 && path[0] == '/' && path[2] == ':' {
			path = path[1:]
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		return Normalized{}, fmt.Errorf("%w: 无法读取本地参考图", ErrInvalidReference)
	}
	if !info.Mode().IsRegular() {
		return Normalized{}, fmt.Errorf("%w: 本地参考图必须是普通文件", ErrInvalidReference)
	}
	if info.Size() > MaxImageBytes {
		return Normalized{}, ErrImageTooLarge
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Normalized{}, fmt.Errorf("%w: 无法读取本地参考图", ErrInvalidReference)
	}
	mimeType := mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}
	return checked(data, mimeType)
}

func fetchHTTP(ctx context.Context, raw string, policy Policy) ([]byte, string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil {
		return nil, "", fmt.Errorf("%w: URL 无效", ErrInvalidReference)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, "", fmt.Errorf("%w: 仅支持 http(s) URL", ErrInvalidReference)
	}
	transport := &http.Transport{Proxy: nil}
	if !policy.AllowPrivateNetwork {
		transport.DialContext = publicDialContext
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   httpTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("%w: 重定向次数过多", ErrInvalidReference)
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return fmt.Errorf("%w: 重定向到不支持的协议", ErrInvalidReference)
			}
			return nil
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, "", fmt.Errorf("%w: URL 无效", ErrInvalidReference)
	}
	req.Header.Set("User-Agent", "Muse2API-Go/2.1")
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, ErrPrivateNetwork) {
			return nil, "", ErrPrivateNetwork
		}
		return nil, "", fmt.Errorf("%w: 参考图下载失败", ErrInvalidReference)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, "", fmt.Errorf("%w: 参考图下载 HTTP %d", ErrInvalidReference, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxImageBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("%w: 参考图下载失败", ErrInvalidReference)
	}
	if len(data) > MaxImageBytes {
		return nil, "", ErrImageTooLarge
	}
	mimeType := resp.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}
	if _, _, err := mime.ParseMediaType(mimeType); err != nil {
		mimeType = http.DetectContentType(data)
	}
	return data, mimeType, nil
}

func publicDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("%w: 地址无效", ErrPrivateNetwork)
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil || len(ips) == 0 {
		return nil, fmt.Errorf("%w: 域名无法解析", ErrInvalidReference)
	}
	var chosen net.IP
	for _, ip := range ips {
		if !isPublicIP(ip) {
			return nil, ErrPrivateNetwork
		}
		if chosen == nil {
			chosen = ip
		}
	}
	return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, net.JoinHostPort(chosen.String(), port))
}

var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),       // "this network"
	netip.MustParsePrefix("100.64.0.0/10"),   // carrier-grade NAT
	netip.MustParsePrefix("192.0.0.0/24"),    // IETF protocol assignments
	netip.MustParsePrefix("192.0.2.0/24"),    // TEST-NET-1
	netip.MustParsePrefix("198.18.0.0/15"),   // benchmarking
	netip.MustParsePrefix("198.51.100.0/24"), // TEST-NET-2
	netip.MustParsePrefix("203.0.113.0/24"),  // TEST-NET-3
	netip.MustParsePrefix("240.0.0.0/4"),     // reserved
	netip.MustParsePrefix("2001:db8::/32"),   // documentation
}

func isPublicIP(ip net.IP) bool {
	if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return false
	}
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	addr = addr.Unmap()
	if !addr.IsGlobalUnicast() {
		return false
	}
	for _, prefix := range blockedPrefixes {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}
