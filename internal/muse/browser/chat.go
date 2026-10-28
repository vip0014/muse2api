package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"muse2api/internal/muse/protocol"
)

type chatState struct {
	Count int    `json:"cnt"`
	Total int    `json:"total"`
	Text  string `json:"txt"`
	Stop  bool   `json:"stop"`
	Path  string `json:"path"`
}

const pollChatJS = `(function(){
	var els=[...document.querySelectorAll('*')].filter(function(e){
		var s=getComputedStyle(e);return (s.overflowY==='auto'||s.overflowY==='scroll')&&e.scrollHeight>e.clientHeight+100;});
	els.sort(function(a,b){return b.scrollHeight-a.scrollHeight;});
	if(els[0])els[0].scrollTop=els[0].scrollHeight;
	var scope=document.querySelector('main,[class*="chat-scroll"],[class*="hatch-chat-scroll"]')||document.body;
	var bs=[].slice.call(scope.querySelectorAll('div[class*="hatch-chat-groupable-bubble"]'))
		.filter(function(b){return /hatch-agent-bubble-bg/.test(b.className||'');});
	var nonEmpty=bs.filter(function(b){return ((b.innerText||'').trim().length)>0;});
	var txt=nonEmpty.length?(nonEmpty[nonEmpty.length-1].innerText||'').trim():'';
	var stop=!!(document.querySelector('[data-testid="hatch-composer-stop-button"]')
		||document.querySelector('button[aria-label*="Stop" i]')
		||document.querySelector('button[aria-label*="停止"]'));
	return JSON.stringify({cnt:nonEmpty.length,total:bs.length,txt:txt,stop:stop,path:location.pathname||''});
})()`

const (
	chatPollInterval    = 60 * time.Millisecond
	chatIdleWithoutStop = 5 * time.Second
	chatIdleAfterStop   = 1500 * time.Millisecond
	chatStopMaxIdle     = 60 * time.Second
)

func chatIdleThreshold(stopSeen bool) time.Duration {
	if stopSeen {
		return chatIdleAfterStop
	}
	return chatIdleWithoutStop
}

func chatShouldFinish(stop, stopSeen bool, idle time.Duration) bool {
	if stop {
		return false
	}
	return idle >= chatIdleThreshold(stopSeen)
}

func (p *Page) chatState(ctx context.Context) (chatState, error) {
	var raw string
	if err := p.conn.JS(ctx, pollChatJS, false, &raw); err != nil {
		return chatState{}, err
	}
	var state chatState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return chatState{}, err
	}
	state.Text = strings.TrimSpace(state.Text)
	return state, nil
}

func jsString(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func (p *Page) clickPoint(ctx context.Context, x, y int) error {
	for _, eventType := range []string{"mousePressed", "mouseReleased"} {
		if err := p.conn.Call(ctx, "Input.dispatchMouseEvent", map[string]any{"type": eventType, "x": x, "y": y, "button": "left", "clickCount": 1}, nil); err != nil {
			return err
		}
	}
	return nil
}

func (p *Page) sendPrompt(ctx context.Context, prompt string) (string, error) {
	if strings.TrimSpace(prompt) == "" {
		return "", errors.New("提示词为空")
	}
	_ = p.conn.JS(ctx, `(function(){var ta=document.querySelector('textarea');if(ta){ta.scrollIntoView({block:'center',inline:'nearest'});ta.focus();}})()`, false, nil)
	var rawPoint string
	if err := p.conn.JS(ctx, `(function(){var t=document.querySelector('textarea');if(!t)return null;var r=t.getBoundingClientRect();return JSON.stringify({x:Math.round(r.left+r.width/2),y:Math.round(r.top+r.height/2)});})()`, false, &rawPoint); err != nil {
		return "", err
	}
	if rawPoint == "" {
		return "", errors.New("找不到聊天输入框")
	}
	var point struct{ X, Y int }
	if err := json.Unmarshal([]byte(rawPoint), &point); err != nil {
		return "", err
	}
	if err := p.clickPoint(ctx, point.X, point.Y); err != nil {
		return "", err
	}
	setter := `(function(t){var ta=document.querySelector('textarea');if(!ta)return 0;ta.focus();var s=Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,'value').set;s.call(ta,t);ta.dispatchEvent(new Event('input',{bubbles:true}));ta.dispatchEvent(new Event('change',{bubbles:true}));return (ta.value||'').length;})(` + jsString(prompt) + `)`
	var length int
	if err := p.conn.JS(ctx, setter, false, &length); err != nil {
		return "", err
	}
	if length == 0 && len(prompt) < 500 {
		if err := p.conn.Call(ctx, "Input.insertText", map[string]any{"text": prompt}, nil); err != nil {
			return "", err
		}
		if err := p.conn.JS(ctx, setter, false, &length); err != nil {
			return "", err
		}
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var clicked string
		if err := p.conn.JS(ctx, `(function(){var b=[...document.querySelectorAll('button,[role=button]')].filter(function(x){return x.offsetParent!==null;}).find(function(x){return /发送|send/i.test(x.getAttribute('aria-label')||'')||/发送|send/i.test(x.getAttribute('data-testid')||'')||/send/i.test(x.innerText||'');});if(!b)return 'no-button';if(b.disabled)return 'disabled';b.click();return 'clicked';})()`, false, &clicked); err != nil {
			return "", err
		}
		if clicked == "clicked" {
			time.Sleep(300 * time.Millisecond)
			return clicked, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(80 * time.Millisecond):
		}
	}
	for _, modifiers := range []int{2, 0} {
		for _, eventType := range []string{"keyDown", "char", "keyUp"} {
			params := map[string]any{"type": eventType, "key": "Enter", "code": "Enter", "windowsVirtualKeyCode": 13, "nativeVirtualKeyCode": 13, "modifiers": modifiers}
			if eventType == "char" {
				params["text"] = "\r"
				params["unmodifiedText"] = "\r"
			}
			if err := p.conn.Call(ctx, "Input.dispatchKeyEvent", params, nil); err != nil {
				return "", err
			}
		}
		time.Sleep(time.Second)
		var empty bool
		if err := p.conn.JS(ctx, `!(document.querySelector('textarea')||{value:''}).value`, false, &empty); err == nil && empty {
			return "enter-sent", nil
		}
	}
	return "no-button", nil
}

func (e *Engine) Chat(ctx context.Context, credentials protocol.AccountCredentials, prompt string, yield func(string) error) (updated protocol.AccountCredentials, returnErr error) {
	e.opMu.Lock()
	defer e.opMu.Unlock()
	updated = cloneCredentials(credentials)
	ctx, cancel := context.WithTimeout(ctx, e.cfg.ChatTimeout)
	defer cancel()
	page, err := e.authenticatedPage(ctx, credentials)
	if err != nil {
		return updated, err
	}
	defer page.close()
	defer func() {
		syncCtx, syncCancel := context.WithTimeout(context.Background(), 5*time.Second)
		updated = page.readCredentials(syncCtx, updated)
		syncCancel()
	}()
	baseline, err := page.chatState(ctx)
	if err != nil {
		return updated, err
	}
	if sentResult, err := page.sendPrompt(ctx, prompt); err != nil {
		return updated, err
	} else if sentResult != "clicked" && sentResult != "enter-sent" {
		return updated, errors.New("提示词发送未确认，已停止等待")
	}
	sentAt := time.Now()
	firstDeadline := sentAt.Add(40 * time.Second)
	sent := ""
	var last string
	lastChange := time.Time{}
	stopSeen := false
	gotFirst := false
	for time.Now().Before(firstDeadline) {
		if err := ctx.Err(); err != nil {
			return updated, err
		}
		time.Sleep(60 * time.Millisecond)
		state, err := page.chatState(ctx)
		if err != nil {
			continue
		}
		current := state.Text
		if current == "" || (state.Count <= baseline.Count && current == baseline.Text) {
			if time.Since(sentAt) > 14*time.Second {
				var tail string
				_ = page.conn.JS(ctx, `document.body ? document.body.innerText.slice(-500) : ''`, false, &tail)
				if strings.Contains(tail, "Still sending") || strings.Contains(tail, "Connecting...") {
					return updated, errors.New("云端 VM 连接超时 (Still sending)")
				}
			}
			continue
		}
		delta := current
		if strings.HasPrefix(current, sent) {
			delta = current[len(sent):]
		}
		if delta != "" {
			sent = current
			if err := yield(delta); err != nil {
				return updated, err
			}
			last = current
			lastChange = time.Now()
			gotFirst = true
			break
		}
	}
	if !gotFirst {
		return updated, errors.New("等待助手首字响应超时")
	}
	for {
		if err := ctx.Err(); err != nil {
			return updated, fmt.Errorf("等待助手回复: %w", err)
		}
		time.Sleep(chatPollInterval)
		state, err := page.chatState(ctx)
		if err != nil || state.Text == "" {
			continue
		}
		if state.Stop {
			stopSeen = true
		}
		current := state.Text
		if current != last {
			delta := current
			if strings.HasPrefix(current, sent) {
				delta = current[len(sent):]
			}
			if delta != "" {
				sent = current
				if err := yield(delta); err != nil {
					return updated, err
				}
			}
			last = current
			lastChange = time.Now()
			continue
		}
		idle := time.Since(lastChange)
		if state.Stop && idle >= chatStopMaxIdle {
			return updated, errors.New("生成长时间无增量，停止按钮仍未消失，回复可能未完成")
		}
		if chatShouldFinish(state.Stop, stopSeen, idle) {
			return updated, nil
		}
	}
}
