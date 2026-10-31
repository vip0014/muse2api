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
	TID          string `json:"tid"`
	Presentation bool   `json:"presentation"`
	HasVideo     bool   `json:"hasVideo"`
	HasImage     bool   `json:"hasImg"`
	Src          string `json:"src"`
	VSrc         string `json:"vSrc"`
	ISrc         string `json:"iSrc"`
	Width        int    `json:"w"`
	Height       int    `json:"h"`
}

// attachmentsJS collects generated media. The official UI renders results in
// hatch-chat-attachment-presentation-* containers; the animated agent avatar
// (hatch_working / hatch_making_something / ...) is also a <video> element and
// used to be picked up as a generation result. Presentation containers are
// therefore treated as the only trusted source; bubble media is a fallback for
// text-delivered results and explicitly excludes avatar/UI animation videos.
const attachmentsJS = `(function(){
	var UI=/(?:avatar|emoji|icon|logo|favicon|sprite|\/videos\/avatars\/|hatch[_-](?:working|connecting|making[_-]?something|milestone|idle))/i;
	var list=[], seenSrc=new Set(), seenEl=new Set();
	function skip(el){try{return !!(el.closest&&el.closest('form, [class*=chat-user-bubble], [class*="group/msg"]'));}catch(e){return false;}}
	function uiAncestor(el){try{return !!(el.closest&&el.closest('[class*="avatar"],[data-testid*="avatar"],[class*="emoji"],[data-testid*="emoji"],[class*="sprite"],[class*="logo"],[class*="icon"]'));}catch(e){return false;}}
	function hidden(el){return !!(el.getAttribute&&el.getAttribute('aria-hidden')==='true');}
	function mediaSrc(m){
		if(!m)return '';
		if(m.tagName==='VIDEO'){
			var s=m.currentSrc||m.src||'';
			if(!s&&m.querySelector){var so=m.querySelector('source[src]');if(so)s=so.src||so.getAttribute('src')||'';}
			return s;
		}
		return m.currentSrc||m.src||'';
	}
	function box(m){return m?[m.videoWidth||m.naturalWidth||m.width||0,m.videoHeight||m.naturalHeight||m.height||0]:[0,0];}
	function displayed(m){try{var r=m.getBoundingClientRect();return [r.width||0,r.height||0];}catch(e){return [0,0];}}
	function addMedia(el,tid,presentation){
		if(!el||seenEl.has(el)||skip(el))return;
		seenEl.add(el);
		var v=el.tagName==='VIDEO'?el:(el.querySelector?el.querySelector('video'):null);
		var img=el.tagName==='IMG'?el:(el.querySelector?el.querySelector('img'):null);
		var primary=v||img;if(!primary)return;
		var src=mediaSrc(primary);
		if(!src||UI.test(src)||hidden(primary)||uiAncestor(primary))return;
		var wh=box(primary),w=wh[0],h=wh[1];
		if(w&&h&&w<100&&h<100)return;
		// Bubble fallback must not pick up the small animated agent avatar; a real
		// result is displayed as a large media card. Presentation containers are
		// exempt because they can be collapsed/offscreen while loading.
		if(!presentation){var d=displayed(primary);if(d[0]>0&&d[1]>0&&(d[0]<160||d[1]<160))return;}
		if(seenSrc.has(src))return;
		seenSrc.add(src);
		list.push({tid:tid||'',presentation:!!presentation,hasVideo:!!v,hasImg:!!img,src:src,vSrc:v?mediaSrc(v):'',iSrc:img?mediaSrc(img):'',w:w,h:h});
	}
	function addBg(el,tid){
		if(!el||seenEl.has(el)||skip(el))return;
		seenEl.add(el);
		var bg='';try{bg=getComputedStyle(el).backgroundImage||'';}catch(e){}
		var m=bg.match(/url\(["']?(.*?)["']?\)/);
		if(!m||!m[1])return;
		var src=m[1];
		if(UI.test(src)||uiAncestor(el))return;
		if(seenSrc.has(src))return;
		seenSrc.add(src);
		list.push({tid:tid||'background',presentation:false,hasVideo:false,hasImg:true,src:src,vSrc:'',iSrc:src,w:0,h:0});
	}
	function frameDocs(root,out){
		out=out||[];
		try{root.querySelectorAll('iframe').forEach(function(f){
			try{var d=f.contentDocument;if(d){out.push(d);frameDocs(d,out);}}catch(e){}
		});}catch(e){}
		return out;
	}
	// Trusted result containers first: dedupe by source keeps the presentation
	// flag even when the same media also appears in the bubble. Generated
	// artifacts may render their media inside a same-origin iframe.
	document.querySelectorAll('[data-testid^="hatch-chat-attachment-presentation-"]').forEach(function(a){
		var tid=a.getAttribute('data-testid')||'presentation';
		addMedia(a,tid,true);
		frameDocs(a).forEach(function(doc){
			try{doc.querySelectorAll('video, img').forEach(function(m){addMedia(m,tid,true);});}catch(e){}
		});
	});
	// Fallback: only the newest agent bubble, so older conversation media and
	// sidebar/avatar animations cannot masquerade as the new result.
	var bubbles=[].slice.call(document.querySelectorAll('div[class*="hatch-agent-bubble-bg"]'));
	var scope=bubbles.length?bubbles[bubbles.length-1]:null;
	if(scope){
		scope.querySelectorAll('video').forEach(function(v){addMedia(v,'agent-video',false);});
		scope.querySelectorAll('img').forEach(function(m){addMedia(m,'agent-image',false);});
		scope.querySelectorAll('div, a').forEach(function(el){addBg(el,'background');});
	}
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

// uiMediaPattern matches the official agent's animated avatar/UI assets. Those
// are <video> elements too, so they must never be reported as generated media.
var uiMediaPattern = regexp.MustCompile(`(?i)(?:avatar|emoji|icon|logo|favicon|sprite|/videos/avatars/|hatch[_-](?:working|connecting|making[_-]?something|milestone|idle))`)

func isUIMediaSource(source string) bool {
	return source != "" && uiMediaPattern.MatchString(source)
}

// mediaExtension returns a lowercase file extension for direct media URLs,
// ignoring query strings and fragments. Blob and signed URLs without a suffix
// return "".
func mediaExtension(source string) string {
	lower := strings.ToLower(strings.TrimSpace(source))
	if i := strings.IndexAny(lower, "?#"); i >= 0 {
		lower = lower[:i]
	}
	for _, ext := range []string{".mp4", ".webm", ".mov", ".m4v", ".png", ".jpg", ".jpeg", ".webp", ".gif"} {
		if strings.HasSuffix(lower, ext) {
			return ext
		}
	}
	return ""
}

func videoSource(a attachment) string {
	if a.VSrc != "" && !isUIMediaSource(a.VSrc) {
		return a.VSrc
	}
	if !isUIMediaSource(a.Src) {
		switch mediaExtension(a.Src) {
		case ".mp4", ".webm", ".mov", ".m4v":
			return a.Src
		}
	}
	return ""
}

func mediaSource(a attachment, expect string) string {
	if expect == "video" {
		return videoSource(a)
	}
	if a.Src != "" {
		return a.Src
	}
	return a.ISrc
}

func matchingAttachment(a attachment, expect string) bool {
	if isUIMediaSource(a.Src) || isUIMediaSource(a.VSrc) {
		return false
	}
	if expect == "video" {
		return a.HasVideo || videoSource(a) != ""
	}
	if a.HasVideo || videoSource(a) != "" {
		return false
	}
	return a.HasImage || a.Src != "" || strings.Contains(strings.ToLower(a.TID), "image")
}

// selectVideoAttachments returns video results newest-last. Once the page has
// any attachment presentation container, only media from those containers is
// trusted: the agent avatar is a plain <video> in the bubble and must never
// win over a real (possibly still loading) presentation result. Direct video
// URLs in a bubble remain a fallback for text-delivered results only when the
// page has no presentation container at all.
func selectVideoAttachments(attachments []attachment) []attachment {
	hasPresentation := false
	for _, item := range attachments {
		if item.Presentation && (item.HasVideo || item.HasImage || item.Src != "") {
			hasPresentation = true
			break
		}
	}
	out := make([]attachment, 0, len(attachments))
	for _, item := range attachments {
		if !matchingAttachment(item, "video") {
			continue
		}
		if item.Presentation {
			out = append(out, item)
			continue
		}
		if !hasPresentation && mediaExtension(mediaSource(item, "video")) != "" {
			out = append(out, item)
		}
	}
	return out
}

const mediaDiagnosticsJS = `(function(){
	var UI=/(?:avatar|emoji|icon|logo|favicon|sprite|\/videos\/avatars\/|hatch[_-](?:working|connecting|making[_-]?something|milestone|idle))/i;
	function ui(el,src){if(UI.test(src||''))return true;try{return !!(el.closest&&el.closest('[class*="avatar"],[data-testid*="avatar"],[class*="emoji"]'));}catch(e){return false;}}
	function docs(root,out){
		out=out||[root];
		try{root.querySelectorAll('iframe').forEach(function(f){
			try{var d=f.contentDocument;if(d){out.push(d);docs(d,out);}}catch(e){}
		});}catch(e){}
		return out;
	}
	var out={uiVideos:0,videos:0,presentations:0,presentationVideos:0,presentationImages:0};
	docs(document).forEach(function(doc){
		doc.querySelectorAll('video').forEach(function(v){
			var src=v.currentSrc||v.src||'';
			if(!src&&v.querySelector){var so=v.querySelector('source[src]');if(so)src=so.src||so.getAttribute('src')||'';}
			if(ui(v,src))out.uiVideos++;else out.videos++;
		});
		doc.querySelectorAll('[data-testid^="hatch-chat-attachment-presentation-"]').forEach(function(a){
			out.presentations++;
			if(a.querySelector('video'))out.presentationVideos++;
			if(a.querySelector('img'))out.presentationImages++;
		});
	});
	return JSON.stringify(out);
})()`

func diagnosticSuffix(diag string) string {
	if diag == "" {
		return ""
	}
	return "；" + diag
}

func (p *Page) mediaDiagnostics(ctx context.Context) string {
	var raw string
	if err := p.conn.JS(ctx, mediaDiagnosticsJS, false, &raw); err != nil {
		return ""
	}
	var d struct {
		UIVideos           int `json:"uiVideos"`
		Videos             int `json:"videos"`
		Presentations      int `json:"presentations"`
		PresentationVideos int `json:"presentationVideos"`
		PresentationImages int `json:"presentationImages"`
	}
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		return ""
	}
	return fmt.Sprintf("页面诊断：附件容器 %d，视频节点 %d（其中头像动画 %d），容器内视频 %d、图片 %d", d.Presentations, d.Videos+d.UIVideos, d.UIVideos, d.PresentationVideos, d.PresentationImages)
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
			if errors.Is(err, context.DeadlineExceeded) {
				diagCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				diag := p.mediaDiagnostics(diagCtx)
				cancel()
				return nil, fmt.Errorf("等待生成超时（stop=%v, bubbles=%d, attachments=%d, text=%q%s）: %w", lastStop, lastCount, lastAttachments, cleanModelText(lastStateText, 80), diagnosticSuffix(diag), err)
			}
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
		if expect == "video" {
			matching = selectVideoAttachments(attachments)
		}
		var candidate *attachment
		source := ""
		if len(matching) > 0 {
			item := matching[len(matching)-1]
			source = mediaSource(item, expect)
			if source != "" && !baseline[source] {
				candidate = &item
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
		// Only accept the attachment after the turn has finished (the stop
		// button is gone) and the source has been stable for two polls. An
		// early partial/placeholder image would otherwise be fetched while the
		// real file is still being written, which hangs extraction and burns the
		// whole task timeout.
		requiredStable := 1
		if candidate != nil && expect == "video" && !candidate.Presentation {
			// A bubble-delivered direct video URL is weaker evidence than an
			// attachment presentation; make it survive three polls.
			requiredStable = 3
		}
		if candidate != nil && !state.Stop {
			if source == stableSrc {
				stableCount++
			} else {
				stableSrc, stableCount = source, 0
			}
			if stableCount >= requiredStable {
				return candidate, nil
			}
		} else if candidate != nil && state.Stop {
			// Some UI builds keep a disabled stop control mounted. A large media
			// source that has not changed for six seconds is still a reliable
			// final-image signal; a small/zero-size placeholder is ignored.
			if source == stableSrc {
				stableCount++
			} else {
				stableSrc, stableCount = source, 0
			}
			if stableCount >= 10 && candidate.Width >= 256 && candidate.Height >= 256 {
				return candidate, nil
			}
		}
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
		// The turn is over but no usable media appeared: fail clearly instead of
		// waiting out the whole generation budget and reporting a bare timeout.
		if expect == "image" && !state.Stop && candidate == nil && time.Since(lastActivity) > 45*time.Second {
			message := "生成已结束但未检测到新图片"
			if text := cleanModelText(state.Text, 200); text != "" {
				message += "：" + text
			}
			diagCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			diag := p.mediaDiagnostics(diagCtx)
			cancel()
			return nil, errors.New(message + diagnosticSuffix(diag))
		}
		if expect == "video" && !state.Stop && candidate == nil && time.Since(lastActivity) > 120*time.Second {
			message := "生成已结束但未检测到视频附件"
			if text := cleanModelText(state.Text, 200); text != "" {
				message += "：" + text
			}
			diagCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			diag := p.mediaDiagnostics(diagCtx)
			cancel()
			return nil, errors.New(message + "（官方页面可能只返回了分享页/静帧）" + diagnosticSuffix(diag))
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
	for attempt := 0; attempt < 2; attempt++ {
		var raw string
		attemptCtx, attemptCancel := context.WithTimeout(ctx, 20*time.Second)
		err := p.conn.JS(attemptCtx, expression, true, &raw)
		attemptCancel()
		if err != nil {
			if IsConnectionError(err) {
				return nil, "", "", err
			}
			lastErr = err
			if ctx.Err() != nil {
				return nil, "", "", ctx.Err()
			}
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
	if isUIMediaSource(source) {
		return nil, errors.New("提取结果是官方界面动画资源，已拒绝作为生成结果")
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

func (p *Page) extractCanvas(ctx context.Context, src string) ([]byte, string, string, error) {
	expression := `(function(src,max){try{var img=[...document.querySelectorAll('img')].find(function(m){return(m.currentSrc||m.src||'')===src;});if(!img)return JSON.stringify({ok:false,err:'no-img'});var w=img.naturalWidth||img.width,h=img.naturalHeight||img.height;if(!w||!h)return JSON.stringify({ok:false,err:'no-size'});var c=document.createElement('canvas');c.width=w;c.height=h;var g=c.getContext('2d');g.drawImage(img,0,0,w,h);var data=c.toDataURL('image/png');var b64=data.split(',')[1]||'';if(!b64)return JSON.stringify({ok:false,err:'empty-data-url'});if(b64.length>max*2)return JSON.stringify({ok:false,err:'media-too-large'});return JSON.stringify({ok:true,mime:'image/png',url:src,b64:b64});}catch(e){return JSON.stringify({ok:false,err:String(e)});}})(` + jsString(src) + `,` + fmt.Sprintf("%d", p.engine.cfg.MaxMediaBytes) + `)`
	var raw string
	if err := p.conn.JS(ctx, expression, false, &raw); err != nil {
		return nil, "", "", err
	}
	var result struct {
		OK   bool   `json:"ok"`
		MIME string `json:"mime"`
		URL  string `json:"url"`
		B64  string `json:"b64"`
		Err  string `json:"err"`
	}
	if err := json.Unmarshal([]byte(raw), &result); err != nil || !result.OK {
		if result.Err != "" {
			return nil, "", "", errors.New("canvas 提取失败: " + result.Err)
		}
		return nil, "", "", errors.New("canvas 提取失败")
	}
	data, err := base64.StdEncoding.DecodeString(result.B64)
	if err != nil {
		return nil, "", "", fmt.Errorf("canvas 数据解码失败: %w", err)
	}
	if len(data) == 0 || int64(len(data)) > p.engine.cfg.MaxMediaBytes {
		return nil, "", "", errors.New("媒体字节为空或超过限制")
	}
	return data, result.MIME, result.URL, nil
}

func (p *Page) downloadFallback(ctx context.Context, src string) ([]byte, string, string, error) {
	before := map[string]bool{}
	entries, _ := os.ReadDir(p.engine.cfg.DownloadDir)
	for _, entry := range entries {
		before[entry.Name()] = true
	}

	// The download control is often hover-only. Move the pointer over the media
	// once so the control exists before the first query.
	var rawRect string
	_ = p.conn.JS(ctx, `(function(src){var m=[...document.querySelectorAll('img,video')].find(function(x){return(x.currentSrc||x.src||'')===src;});if(!m)return '';m.scrollIntoView({block:'center',inline:'nearest'});var r=m.getBoundingClientRect();return JSON.stringify({x:Math.round(r.left+r.width/2),y:Math.round(r.top+r.height/2)});})(`+jsString(src)+`)`, false, &rawRect)
	if rawRect != "" {
		var point struct{ X, Y int }
		if json.Unmarshal([]byte(rawRect), &point) == nil {
			_ = p.conn.Call(ctx, "Input.dispatchMouseEvent", map[string]any{"type": "mouseMoved", "x": point.X, "y": point.Y}, nil)
			select {
			case <-ctx.Done():
				return nil, "", "", ctx.Err()
			case <-time.After(300 * time.Millisecond):
			}
		}
	}

	downloadButtonJS := `(function(src){var media=[...document.querySelectorAll('img,video')].find(function(m){return(m.currentSrc||m.src||'')===src;});if(!media)return 'none';var node=media.closest('[data-testid^="hatch-chat-attachment-presentation-"]')||media.closest('[class*="group/widget-presentation"]')||media.parentElement;if(!node)return 'none';var buttons=[...node.querySelectorAll('button,[role=button],a')];var b=buttons.find(function(x){var hay=[x.getAttribute('aria-label'),x.getAttribute('title'),x.getAttribute('data-testid'),x.getAttribute('href'),x.innerText].join(' ');return /下载|保存|download|save/i.test(hay);});if(!b)return 'none';b.click();return 'ok';})(` + jsString(src) + `)`

	clicked := ""
	buttonDeadline := time.Now().Add(10 * time.Second)
	for {
		if err := ctx.Err(); err != nil {
			return nil, "", "", err
		}
		err := p.conn.JS(ctx, downloadButtonJS, false, &clicked)
		if err != nil {
			if IsConnectionError(err) {
				return nil, "", "", err
			}
			if ctx.Err() != nil {
				return nil, "", "", ctx.Err()
			}
		} else if clicked == "ok" {
			break
		}
		if time.Now().After(buttonDeadline) {
			return nil, "", "", errors.New("未找到对应附件下载按钮")
		}
		select {
		case <-ctx.Done():
			return nil, "", "", ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}

	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return nil, "", "", err
		}
		select {
		case <-ctx.Done():
			return nil, "", "", ctx.Err()
		case <-time.After(1500 * time.Millisecond):
		}
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

func (e *Engine) generationTimeout(expect string) time.Duration {
	timeout := e.cfg.ImageTimeout
	if expect == "video" {
		timeout = e.cfg.VideoTimeout
	}
	if timeout <= 0 {
		timeout = 240 * time.Second
	}
	return timeout
}

func extractionReserve(expect string) time.Duration {
	if expect == "video" {
		return 120 * time.Second
	}
	return 60 * time.Second
}

// Generate retries one connection-level failure that happened before the
// prompt was submitted. The retry uses a fresh page (and, when needed, a fresh
// browser connection) and shares the original task deadline. The overall
// deadline includes a separate extraction reserve so a late image still has
// time to be fetched.
func (e *Engine) Generate(ctx context.Context, credentials protocol.AccountCredentials, prompt, expect string, references []string) (result *protocol.ImageResult, updated protocol.AccountCredentials, returnErr error) {
	e.opMu.Lock()
	defer e.opMu.Unlock()
	timeout := e.generationTimeout(expect)
	ctx, cancel := context.WithTimeout(ctx, timeout+extractionReserve(expect))
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

// generateOnce assumes opMu is already held by Generate. The generation timeout
// starts after the lock is acquired, so time spent queueing behind another
// browser task does not consume the generation budget.
func (e *Engine) generateOnce(ctx context.Context, credentials protocol.AccountCredentials, prompt, expect string, references []string) (result *protocol.ImageResult, updated protocol.AccountCredentials, returnErr error) {
	genCtx, genCancel := context.WithTimeout(ctx, e.generationTimeout(expect))
	defer genCancel()
	updated = cloneCredentials(credentials)
	page, err := e.authenticatedPage(genCtx, credentials)
	if err != nil {
		return nil, updated, stageErr(stageConnect, err)
	}
	defer page.close()
	defer func() {
		syncCtx, syncCancel := context.WithTimeout(context.Background(), 5*time.Second)
		updated = page.readCredentials(syncCtx, updated)
		syncCancel()
	}()
	_ = page.conn.JS(genCtx, scrollBottomJS, false, nil)
	if len(references) > 0 {
		if err := page.attachImages(genCtx, references); err != nil {
			return nil, updated, stageErr(stageConnect, err)
		}
	} else {
		page.clearAttachments(genCtx)
	}
	before, err := page.attachments(genCtx)
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
	state, err := page.chatState(genCtx)
	if err != nil {
		return nil, updated, stageErr(stageConnect, err)
	}
	clicked, err := page.sendPrompt(genCtx, prompt)
	if err != nil {
		return nil, updated, stageErr(stagePrompt, err)
	}
	if clicked != "clicked" && clicked != "enter-sent" {
		return nil, updated, stageErr(stagePrompt, errors.New("提示词发送未确认，已停止生成"))
	}
	found, err := page.waitAttachment(genCtx, baseline, state.Count, len(before), expect)
	if err != nil {
		if err == context.DeadlineExceeded {
			err = fmt.Errorf("等待生成结果超时: %w", err)
		}
		return nil, updated, stageErr(stageWait, err)
	}
	source := mediaSource(*found, expect)
	extractCtx, extractCancel := context.WithTimeout(ctx, extractionReserve(expect))
	defer extractCancel()
	data, mimeType, finalURL, extractErr := page.extractBytes(extractCtx, source)
	if extractErr != nil && expect == "image" {
		data, mimeType, finalURL, extractErr = page.extractCanvas(extractCtx, source)
	}
	if extractErr != nil {
		data, mimeType, finalURL, extractErr = page.downloadFallback(extractCtx, source)
	}
	if extractErr != nil {
		return nil, updated, stageErr(stageExtract, fmt.Errorf("已生成但未能取回媒体文件: %w", extractErr))
	}
	result, err = e.saveMedia(data, mimeType, finalURL, expect)
	return result, updated, stageErr(stageExtract, err)
}
