package browser

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"muse2api/internal/muse/protocol"
	"muse2api/internal/refimage"
)

type attachment struct {
	TID      string `json:"tid"`
	HasVideo bool   `json:"hasVideo"`
	HasImage bool   `json:"hasImg"`
	Src      string `json:"src"`
	VSrc     string `json:"vSrc"`
	ISrc     string `json:"iSrc"`
	Width    int    `json:"w"`
	Height   int    `json:"h"`
}

const attachmentsJS = `(function(){
	var list=[];var seen=new Set();
	function addEl(el,tid){
		if(!el||seen.has(el))return;seen.add(el);
		if(el.closest('form, [class*=chat-user-bubble], [class*="group/msg"]'))return;
		var v=el.querySelector('video')||(el.tagName==='VIDEO'?el:null);
		var img=el.querySelector('img')||(el.tagName==='IMG'?el:null);
		var isVid=(tid||'').includes('video')||!!v;var primary=isVid?(v||img):(img||v);
		var src=primary?(primary.currentSrc||primary.src||''):'';
		if(src&&!seen.has(src)){seen.add(src);list.push({tid:tid||el.getAttribute('data-testid')||(isVid?'video':'image'),hasVideo:!!v,hasImg:!!img,src:src,vSrc:v?(v.currentSrc||v.src||''):'',iSrc:img?(img.currentSrc||img.src||''):'',w:primary?(primary.videoWidth||primary.naturalWidth||0):0,h:primary?(primary.videoHeight||primary.naturalHeight||0):0});}
	}
	document.querySelectorAll('[data-testid^="hatch-chat-attachment-presentation-"]').forEach(function(a){addEl(a,a.getAttribute('data-testid'));});
	document.querySelectorAll('div[class*="hatch-agent-bubble-bg"] img, div[class*="hatch-agent-bubble-bg"] video').forEach(function(m){var s=m.currentSrc||m.src||'';if(s&&!s.includes('avatar')&&!s.includes('emoji'))addEl(m.parentElement||m,'agent-media');});
	return JSON.stringify(list);
})()`

const scrollBottomJS = `(function(){var els=[...document.querySelectorAll('*')].filter(function(e){var s=getComputedStyle(e);return(s.overflowY==='auto'||s.overflowY==='scroll')&&e.scrollHeight>e.clientHeight+100;});els.sort(function(a,b){return b.scrollHeight-a.scrollHeight;});if(els[0])els[0].scrollTop=els[0].scrollHeight;var s=document.scrollingElement||document.body;s.scrollTop=s.scrollHeight;var el=document.querySelector('textarea');if(el)el.scrollIntoView({block:'end'});return 1;})()`

func (p *Page) attachments(ctx context.Context) ([]attachment, error) {
	var raw string
	if err := p.conn.JS(ctx, attachmentsJS, false, &raw); err != nil {
		return nil, err
	}
	var out []attachment
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (p *Page) clearAttachments(ctx context.Context) {
	_ = p.conn.JS(ctx, `(function(){var btns=Array.from(document.querySelectorAll('button[aria-label]')).filter(function(b){var al=b.getAttribute('aria-label')||'';return /remove\s*attachment|移除附件|删除附件|移除|删除/i.test(al);});btns.forEach(function(b){b.click();});Array.from(document.querySelectorAll('input[type="file"]')).forEach(function(inp){inp.value='';});return btns.length;})()`, false, nil)
	time.Sleep(300 * time.Millisecond)
}

func (p *Page) composerAttachmentCount(ctx context.Context) (int, error) {
	var raw string
	err := p.conn.JS(ctx, `(function(){
		var ov = document.querySelector('[data-testid="hatch-composer-placeholder-overlay"]');
		var composer = ov ? (ov.closest('form') || ov.parentElement.parentElement.parentElement) : null;
		if (!composer) return 0;
		var remove = Array.from(composer.querySelectorAll('button[aria-label]')).filter(function(b){
			return /remove\s*attachment|移除附件|删除附件/i.test(b.getAttribute('aria-label') || '');
		}).length;
		if (remove) return remove;
		return composer.querySelectorAll('img, video').length;
	})()`, false, &raw)
	if err != nil {
		return 0, err
	}
	var count int
	if err := json.Unmarshal([]byte(raw), &count); err != nil {
		return 0, err
	}
	return count, nil
}

func (p *Page) attachImages(ctx context.Context, references []string) error {
	if len(references) == 0 {
		return nil
	}
	if len(references) > refimage.MaxImages {
		return refimage.ErrTooManyImages
	}
	p.clearAttachments(ctx)
	totalBytes := 0
	for index, raw := range references {
		data, mimeType, err := refimage.DecodeDataURL(raw)
		if err != nil {
			return fmt.Errorf("第 %d 张参考图：%w", index+1, err)
		}
		totalBytes += len(data)
		if totalBytes > refimage.MaxTotalBytes {
			return refimage.ErrTotalTooLarge
		}
		parts := strings.SplitN(strings.ToLower(mimeType), "/", 2)
		ext := "png"
		if len(parts) == 2 && parts[1] != "" {
			ext = strings.TrimPrefix(parts[1], "x-")
		}
		if ext == "jpeg" {
			ext = "jpg"
		}
		name := fmt.Sprintf("reference_image_%d.%s", index+1, ext)
		encoded := base64.StdEncoding.EncodeToString(data)
		expression := `(function(b64,mime,name){try{var chars=atob(b64);var nums=new Array(chars.length);for(var i=0;i<chars.length;i++)nums[i]=chars.charCodeAt(i);var blob=new Blob([new Uint8Array(nums)],{type:mime});var file=new File([blob],name,{type:mime});var inputs=Array.from(document.querySelectorAll('input[type="file"]'));if(!inputs.length)return JSON.stringify({ok:false,err:'no-file-input'});var ov=document.querySelector('[data-testid="hatch-composer-placeholder-overlay"]');var composer=ov?ov.closest('form'):null;var input=composer?inputs.find(function(x){return composer.contains(x);}):null;if(!input)input=inputs.find(function(x){return !x.closest('[class*=chat-user-bubble], [class*="group/msg"]');})||inputs[0];if(!input.getAttribute('accept'))input.setAttribute('accept','image/*');var dt=new DataTransfer();dt.items.add(file);input.files=dt.files;input.dispatchEvent(new Event('change',{bubbles:true}));input.dispatchEvent(new Event('input',{bubbles:true}));return JSON.stringify({ok:true});}catch(e){return JSON.stringify({ok:false,err:String(e)});}})(` + jsString(encoded) + `,` + jsString(mimeType) + `,` + jsString(name) + `)`
		var rawResult string
		if err := p.conn.JS(ctx, expression, false, &rawResult); err != nil {
			return fmt.Errorf("第 %d 张参考图附加失败：%w", index+1, err)
		}
		var result struct {
			OK bool `json:"ok"`
		}
		if err := json.Unmarshal([]byte(rawResult), &result); err != nil || !result.OK {
			return fmt.Errorf("第 %d 张参考图附加失败，已停止生成", index+1)
		}
		deadline := time.Now().Add(15 * time.Second)
		for {
			count, err := p.composerAttachmentCount(ctx)
			if err == nil && count >= index+1 {
				break
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("参考图上传未确认（%d/%d），已停止生成", count, len(references))
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(300 * time.Millisecond):
			}
		}
		time.Sleep(350 * time.Millisecond)
	}
	time.Sleep(500 * time.Millisecond)
	return nil
}

func (p *Page) attachImage(ctx context.Context, raw string) error {
	return p.attachImages(ctx, []string{raw})
}

func mediaSource(a attachment, expect string) string {
	if expect == "video" && a.VSrc != "" {
		return a.VSrc
	}
	return a.Src
}

func matchingAttachment(a attachment, expect string) bool {
	src := strings.ToLower(a.Src)
	if expect == "video" {
		return a.HasVideo || a.VSrc != "" || strings.HasSuffix(src, ".mp4") || strings.HasSuffix(src, ".webm") || strings.HasSuffix(src, ".mov")
	}
	return strings.Contains(strings.ToLower(a.TID), "image") || (!a.HasVideo && !strings.HasSuffix(src, ".mp4") && !strings.HasSuffix(src, ".webm") && !strings.HasSuffix(src, ".mov"))
}

var mediaReportPattern = regexp.MustCompile(`(?i)\.(webp|png|jpe?g|mp4|webm)|imagine_media|deliverable|generated\s+.*image|verified\s+generated|artifact`)
var deliveryErrorPattern = regexp.MustCompile(`(?i)额度不足|积分不足|out of credits|达到上限|token limit`)
var stuckDeliveryPattern = regexp.MustCompile(`(?i)Still sending|Delivery not confirmed|正在发送|未确认送达`)

var imageRefusalPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^(?:图片没生成成功|图片未生成成功|图片生成失败|无法生成图片)`),
	regexp.MustCompile(`(?i)^(?:could not|couldn't|cannot|can't|unable to).{0,120}image`),
}

var videoRefusalPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^(?:没生成成|视频没生成成功|视频未生成成功|视频生成失败).{0,240}(?:国家|地区|country|region).{0,160}(?:不支持|不可用|无法|not available|unavailable|not supported)`),
	regexp.MustCompile(`(?i)^(?:could not|couldn't|cannot|can't|unable to).{0,120}video.{0,160}(?:country|region|not available|unavailable|not supported)`),
}

func cleanModelText(text string, limit int) string {
	clean := strings.Join(strings.Fields(text), " ")
	if limit <= 0 {
		return clean
	}
	runes := []rune(clean)
	if len(runes) > limit {
		return string(runes[:limit])
	}
	return clean
}

func mediaRefusalText(text, expect string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	patterns := imageRefusalPatterns
	if expect == "video" {
		patterns = videoRefusalPatterns
	}
	lines := strings.Split(text, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		clean := cleanModelText(lines[i], 240)
		if clean == "" {
			continue
		}
		for _, pattern := range patterns {
			if pattern.MatchString(clean) {
				return clean
			}
		}
	}
	return ""
}

func (p *Page) waitAttachment(ctx context.Context, baseline map[string]bool, baseAgent, baseAttachments int, expect string) (*attachment, error) {
	stableSrc := ""
	stableCount := 0
	lastStateText := ""
	stableText := ""
	textStable := 0
	refusalLast := ""
	refusalStable := 0
	seenStop := false
	lastActivity := time.Now()
	lastCount, lastAttachments, lastStop, lastPath := -1, -1, false, ""
	started := time.Now()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		time.Sleep(600 * time.Millisecond)
		_ = p.conn.JS(ctx, scrollBottomJS, false, nil)
		attachments, err := p.attachments(ctx)
		if err != nil {
			if IsConnectionError(err) {
				return nil, err
			}
			continue
		}
		matching := make([]attachment, 0, len(attachments))
		for _, item := range attachments {
			if matchingAttachment(item, expect) {
				matching = append(matching, item)
			}
		}
		if len(matching) > 0 {
			candidate := matching[len(matching)-1]
			source := mediaSource(candidate, expect)
			if expect == "video" && candidate.VSrc == "" {
				lower := strings.ToLower(source)
				if !strings.HasSuffix(lower, ".mp4") && !strings.HasSuffix(lower, ".webm") && !strings.HasSuffix(lower, ".mov") {
					source = ""
				}
			}
			if source != "" && !baseline[source] {
				if candidate.Width > 0 && candidate.Height > 0 {
					return &candidate, nil
				}
				if source == stableSrc {
					stableCount++
				} else {
					stableSrc, stableCount = source, 0
				}
				if stableCount >= 1 {
					return &candidate, nil
				}
			}
		}
		state, err := p.chatState(ctx)
		if err != nil {
			if IsConnectionError(err) {
				return nil, err
			}
			continue
		}
		if state.Count != lastCount || state.Text != lastStateText || state.Stop != lastStop || state.Path != lastPath || len(attachments) != lastAttachments {
			lastActivity = time.Now()
			lastCount, lastStateText, lastStop, lastPath, lastAttachments = state.Count, state.Text, state.Stop, state.Path, len(attachments)
		}
		seenStop = seenStop || state.Stop
		var tail string
		_ = p.conn.JS(ctx, `document.body ? document.body.innerText.slice(-700) : ''`, false, &tail)
		if deliveryErrorPattern.MatchString(tail) {
			return nil, errors.New("账号额度不足")
		}
		if time.Since(started) > 30*time.Second && state.Count == baseAgent && stuckDeliveryPattern.MatchString(tail) {
			return nil, errors.New("提示词未成功送达后端，终止等待")
		}
		refusal := mediaRefusalText(state.Text, expect)
		if refusal != "" && !state.Stop && len(attachments) <= baseAttachments {
			if refusal == refusalLast {
				refusalStable++
			} else {
				refusalLast, refusalStable = refusal, 0
			}
			if refusalStable >= 4 && time.Since(started) > 8*time.Second {
				return nil, &protocol.ModelRefusalError{Message: refusal}
			}
		} else {
			refusalLast, refusalStable = "", 0
		}
		if state.Count > baseAgent && state.Text != "" && !state.Stop && len(attachments) <= baseAttachments && !mediaReportPattern.MatchString(state.Text) {
			if state.Text == stableText {
				textStable++
			} else {
				stableText, textStable = state.Text, 0
			}
			if textStable >= 15 && time.Since(started) > 8*time.Second {
				return nil, &protocol.ModelRefusalError{Message: cleanModelText(state.Text, 240)}
			}
		} else {
			textStable = 0
		}
		if expect == "video" && seenStop && !state.Stop &&
			strings.HasSuffix(strings.TrimRight(state.Path, "/"), "/thread/new") &&
			state.Count <= baseAgent && len(attachments) <= baseAttachments &&
			time.Since(started) >= 180*time.Second && time.Since(lastActivity) >= 90*time.Second {
			return nil, errors.New("Muse 未建立视频任务线程，页面在提交结束后持续无响应（已提前停止等待）")
		}
	}
}

func (p *Page) extractBytes(ctx context.Context, src string) ([]byte, string, string, error) {
	expression := `(async function(src,max){try{if(!src)return JSON.stringify({ok:false,err:'no-media-src'});var r=await fetch(src);if(!r.ok)return JSON.stringify({ok:false,err:'media-http-'+r.status});var b=await r.blob();if(b.size>max)return JSON.stringify({ok:false,err:'media-too-large'});var ab=await b.arrayBuffer();var bytes=new Uint8Array(ab);var s='';for(var i=0;i<bytes.length;i+=65536)s+=String.fromCharCode.apply(null,bytes.subarray(i,i+65536));return JSON.stringify({ok:true,mime:b.type||'',size:b.size,url:src,b64:btoa(s)});}catch(e){return JSON.stringify({ok:false,err:String(e)});}})(` + jsString(src) + `,` + fmt.Sprintf("%d", p.engine.cfg.MaxMediaBytes) + `)`
	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		var raw string
		if err := p.conn.JS(ctx, expression, true, &raw); err != nil {
			if IsConnectionError(err) {
				return nil, "", "", err
			}
			lastErr = err
		} else {
			var result struct {
				OK   bool   `json:"ok"`
				MIME string `json:"mime"`
				URL  string `json:"url"`
				B64  string `json:"b64"`
				Err  string `json:"err"`
			}
			if err := json.Unmarshal([]byte(raw), &result); err == nil && result.OK {
				data, err := base64.StdEncoding.DecodeString(result.B64)
				if err == nil && len(data) > 0 && int64(len(data)) <= p.engine.cfg.MaxMediaBytes {
					return data, result.MIME, result.URL, nil
				}
				lastErr = errors.New("媒体字节为空或超过限制")
			} else {
				lastErr = errors.New("页面媒体提取失败")
			}
		}
		select {
		case <-ctx.Done():
			return nil, "", "", ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return nil, "", "", lastErr
}

func randomMediaName(ext string) (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf) + ext, nil
}

func detectMediaType(data []byte) (kind, mimeType, ext string) {
	switch {
	case len(data) >= 8 && bytes.Equal(data[:8], []byte("\x89PNG\r\n\x1a\n")):
		return "image", "image/png", ".png"
	case len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff:
		return "image", "image/jpeg", ".jpg"
	case len(data) >= 6 && (bytes.Equal(data[:6], []byte("GIF87a")) || bytes.Equal(data[:6], []byte("GIF89a"))):
		return "image", "image/gif", ".gif"
	case len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")):
		return "image", "image/webp", ".webp"
	case len(data) >= 12 && bytes.Equal(data[4:8], []byte("ftyp")):
		brand := string(data[8:12])
		if brand == "avif" || brand == "avis" {
			return "image", "image/avif", ".avif"
		}
		return "video", "video/mp4", ".mp4"
	case len(data) >= 4 && bytes.Equal(data[:4], []byte{0x1a, 0x45, 0xdf, 0xa3}):
		return "video", "video/webm", ".webm"
	}
	return "", "", ""
}

func pickExtension(mimeType, source, expect string) string {
	mimeType = strings.ToLower(mimeType)
	for _, item := range []struct{ key, ext string }{{"mp4", ".mp4"}, {"webm", ".webm"}, {"png", ".png"}, {"jpeg", ".jpg"}, {"jpg", ".jpg"}, {"webp", ".webp"}, {"gif", ".gif"}} {
		if strings.Contains(mimeType, item.key) {
			return item.ext
		}
	}
	lower := strings.ToLower(source)
	for _, ext := range []string{".mp4", ".webm", ".png", ".jpg", ".webp"} {
		if strings.Contains(lower, ext) {
			return ext
		}
	}
	if expect == "video" {
		return ".mp4"
	}
	return ".png"
}

func (e *Engine) saveMedia(data []byte, _ string, source, expect string) (*protocol.ImageResult, error) {
	if len(data) == 0 || int64(len(data)) > e.cfg.MaxMediaBytes {
		return nil, errors.New("媒体内容为空或超过大小限制")
	}
	kind, _, ext := detectMediaType(data)
	if kind == "" {
		return nil, errors.New("无法识别媒体类型，拒绝交付不可验证的文件")
	}
	if kind != expect {
		return nil, fmt.Errorf("期望生成 %s，但提取结果为 %s，拒绝交付错误类型", expect, kind)
	}
	name, err := randomMediaName(ext)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(e.cfg.MediaDir, 0o700); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(e.cfg.MediaDir, ".download-*")
	if err != nil {
		return nil, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err == nil {
		_, err = tmp.Write(data)
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	if err := os.Rename(tmpName, filepath.Join(e.cfg.MediaDir, name)); err != nil {
		return nil, err
	}
	base := strings.TrimRight(e.cfg.PublicBase, "/")
	result := &protocol.ImageResult{Filename: name, Kind: kind, URL: base + "/v1/media/" + name, Bytes: int64(len(data))}
	if kind == "video" {
		if durationMS, width, height, ok := probeMP4Metadata(data); ok {
			result.DurationMS, result.Width, result.Height = durationMS, width, height
		}
	}
	return result, nil
}

func (p *Page) downloadFallback(ctx context.Context, src string) ([]byte, string, string, error) {
	before := map[string]bool{}
	entries, _ := os.ReadDir(p.engine.cfg.DownloadDir)
	for _, entry := range entries {
		before[entry.Name()] = true
	}
	var clicked string
	if err := p.conn.JS(ctx, `(function(src){var media=[...document.querySelectorAll('img,video')].find(function(m){return(m.currentSrc||m.src||'')===src;});var node=media&&media.closest('[data-testid^="hatch-chat-attachment-presentation-"]');if(!node)return 'none';node=node.closest('[class*="group/widget-presentation"]')||node;var b=[...node.querySelectorAll('button,[role=button]')].find(function(x){return /下载|保存|download/i.test(x.getAttribute('aria-label')||'');});if(!b)return 'none';b.click();return 'ok';})(`+jsString(src)+`)`, false, &clicked); err != nil || clicked != "ok" {
		return nil, "", "", errors.New("未找到对应附件下载按钮")
	}
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		time.Sleep(1500 * time.Millisecond)
		entries, _ := os.ReadDir(p.engine.cfg.DownloadDir)
		var candidates []string
		for _, entry := range entries {
			if entry.Type().IsRegular() && !before[entry.Name()] && !strings.HasSuffix(entry.Name(), ".crdownload") {
				candidates = append(candidates, entry.Name())
			}
		}
		sort.Strings(candidates)
		if len(candidates) > 0 {
			name := candidates[len(candidates)-1]
			path := filepath.Join(p.engine.cfg.DownloadDir, name)
			file, err := os.Open(path)
			if err != nil {
				continue
			}
			data, readErr := io.ReadAll(io.LimitReader(file, p.engine.cfg.MaxMediaBytes+1))
			_ = file.Close()
			_ = os.Remove(path)
			if readErr == nil && len(data) > 0 && int64(len(data)) <= p.engine.cfg.MaxMediaBytes {
				return data, "", name, nil
			}
		}
	}
	return nil, "", "", errors.New("附件下载超时")
}

type generationStage string

const (
	stageConnect generationStage = "connect"
	stagePrompt  generationStage = "prompt"
	stageWait    generationStage = "wait"
	stageExtract generationStage = "extract"
)

type stageError struct {
	stage generationStage
	err   error
}

func (e *stageError) Error() string { return e.err.Error() }
func (e *stageError) Unwrap() error { return e.err }

func stageErr(stage generationStage, err error) error {
	if err == nil {
		return nil
	}
	return &stageError{stage: stage, err: err}
}

// retryableBeforeSubmit deliberately excludes prompt/wait/extract stages: once
// the prompt is submitted the upstream may still be generating, so an
// automatic retry could duplicate a paid generation.
func retryableBeforeSubmit(err error) bool {
	var staged *stageError
	if !errors.As(err, &staged) || staged.stage != stageConnect {
		return false
	}
	return IsRetryable(staged.err)
}

// Generate retries one connection-level failure that happened before the
// prompt was submitted. The retry uses a fresh page (and, when needed, a fresh
// browser connection) and shares the original task deadline.
func (e *Engine) Generate(ctx context.Context, credentials protocol.AccountCredentials, prompt, expect string, references []string) (result *protocol.ImageResult, updated protocol.AccountCredentials, returnErr error) {
	e.opMu.Lock()
	defer e.opMu.Unlock()
	timeout := e.cfg.ImageTimeout
	if expect == "video" {
		timeout = e.cfg.VideoTimeout
	}
	if timeout <= 0 {
		timeout = 240 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	updated = cloneCredentials(credentials)
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		// Carry refreshed cookies from a failed pre-submit attempt into the
		// retry; the deferred credential sync may already have rotated them.
		result, updated, returnErr = e.generateOnce(ctx, updated, prompt, expect, references)
		if returnErr == nil {
			return result, updated, nil
		}
		lastErr = returnErr
		if ctx.Err() != nil || !retryableBeforeSubmit(returnErr) {
			return nil, updated, returnErr
		}
		select {
		case <-ctx.Done():
			return nil, updated, ctx.Err()
		case <-time.After(750 * time.Millisecond):
		}
	}
	return nil, updated, lastErr
}

// generateOnce assumes opMu is already held by Generate. The timeout also
// starts after the lock is acquired, so time spent queueing behind another
// browser task does not consume the generation budget.
func (e *Engine) generateOnce(ctx context.Context, credentials protocol.AccountCredentials, prompt, expect string, references []string) (result *protocol.ImageResult, updated protocol.AccountCredentials, returnErr error) {
	updated = cloneCredentials(credentials)
	page, err := e.authenticatedPage(ctx, credentials)
	if err != nil {
		return nil, updated, stageErr(stageConnect, err)
	}
	defer page.close()
	defer func() {
		syncCtx, syncCancel := context.WithTimeout(context.Background(), 5*time.Second)
		updated = page.readCredentials(syncCtx, updated)
		syncCancel()
	}()
	_ = page.conn.JS(ctx, scrollBottomJS, false, nil)
	if len(references) > 0 {
		if err := page.attachImages(ctx, references); err != nil {
			return nil, updated, stageErr(stageConnect, err)
		}
	} else {
		page.clearAttachments(ctx)
	}
	before, err := page.attachments(ctx)
	if err != nil {
		return nil, updated, stageErr(stageConnect, err)
	}
	baseline := map[string]bool{}
	for _, item := range before {
		for _, source := range []string{item.Src, item.VSrc, item.ISrc} {
			if source != "" {
				baseline[source] = true
			}
		}
	}
	state, err := page.chatState(ctx)
	if err != nil {
		return nil, updated, stageErr(stageConnect, err)
	}
	clicked, err := page.sendPrompt(ctx, prompt)
	if err != nil {
		return nil, updated, stageErr(stagePrompt, err)
	}
	if clicked != "clicked" && clicked != "enter-sent" {
		return nil, updated, stageErr(stagePrompt, errors.New("提示词发送未确认，已停止生成"))
	}
	found, err := page.waitAttachment(ctx, baseline, state.Count, len(before), expect)
	if err != nil {
		return nil, updated, stageErr(stageWait, err)
	}
	source := mediaSource(*found, expect)
	data, mimeType, finalURL, extractErr := page.extractBytes(ctx, source)
	if extractErr != nil {
		data, mimeType, finalURL, extractErr = page.downloadFallback(ctx, source)
	}
	if extractErr != nil {
		return nil, updated, stageErr(stageExtract, fmt.Errorf("已生成但未能取回媒体文件: %w", extractErr))
	}
	result, err = e.saveMedia(data, mimeType, finalURL, expect)
	return result, updated, stageErr(stageExtract, err)
}
