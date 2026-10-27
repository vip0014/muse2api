package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"muse2api/internal/model"
	"muse2api/internal/muse/protocol"
)

const quotaSettingsJS = `(() => {const visible=e=>e.getClientRects().length>0&&getComputedStyle(e).visibility!=='hidden';const labels=['Settings','設定','设置'];const b=[...document.querySelectorAll('[data-testid="hatch-dock-more"]')].find(visible)||[...document.querySelectorAll('button,[role=button]')].find(e=>visible(e)&&labels.includes((e.getAttribute('aria-label')||'').trim()));if(!b)return null;const r=b.getBoundingClientRect();return JSON.stringify({x:Math.round(r.x+r.width/2),y:Math.round(r.y+r.height/2)});})()`
const quotaMenuJS = `(() => {const visible=e=>e.getClientRects().length>0&&getComputedStyle(e).visibility!=='hidden';const labels=['Settings','設定','设置'];const exact=e=>labels.includes((e.textContent||'').trim());const b=[...document.querySelectorAll('[role=menuitem]')].find(e=>visible(e)&&(exact(e)||[...e.querySelectorAll('span,div')].some(exact)))||[...document.querySelectorAll('[role=menu] button,[role=menu] span,[role=menu] div')].find(e=>visible(e)&&exact(e));if(!b)return null;const r=b.getBoundingClientRect();return JSON.stringify({x:Math.round(r.x+r.width/2),y:Math.round(r.y+r.height/2)});})()`
const quotaUsageJS = `(() => {const visible=e=>e.getClientRects().length>0&&getComputedStyle(e).visibility!=='hidden';const labels=['Usage','使用情况'];const b=[...document.querySelectorAll('h2,h3,button,[role=button],[role=menuitem]')].find(e=>visible(e)&&labels.includes((e.textContent||'').trim()));if(!b)return null;const r=b.getBoundingClientRect();return JSON.stringify({x:Math.round(r.x+r.width/2),y:Math.round(r.y+r.height/2)});})()`
const quotaDialogJS = `(() => {const d=[...document.querySelectorAll('[role=dialog],[aria-modal=true]')].find(e=>e.getClientRects().length>0&&getComputedStyle(e).visibility!=='hidden');return d?(d.innerText||''):'';})()`

func (p *Page) dismissQuotaUI(ctx context.Context) {
	for i := 0; i < 2; i++ {
		for _, eventType := range []string{"keyDown", "keyUp"} {
			_ = p.conn.Call(ctx, "Input.dispatchKeyEvent", map[string]any{"type": eventType, "key": "Escape", "code": "Escape", "windowsVirtualKeyCode": 27, "nativeVirtualKeyCode": 27}, nil)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// waitQuotaReady waits only for the settings control, not the chat textarea.
// The quota panel lives behind the account menu and can open while the chat VM
// is still connecting, which removes a large chunk of first-refresh latency.
func (p *Page) waitQuotaReady(ctx context.Context) error {
	deadline := time.Now().Add(p.engine.cfg.LoginWait)
	for time.Now().Before(deadline) {
		var state string
		err := p.conn.JS(ctx, `(function(){
			if (document.readyState !== 'complete') return 'loading';
			var h = document.querySelector('[data-hatch-shell-hydration-state]');
			if (h && h.getAttribute('data-hatch-shell-hydration-state') !== 'hydrated') return 'hydrating';
			const visible=e=>e.getClientRects().length>0&&getComputedStyle(e).visibility!=='hidden';
			const labels=['Settings','設定','设置'];
			const b=[...document.querySelectorAll('[data-testid="hatch-dock-more"]')].find(visible)||[...document.querySelectorAll('button,[role=button]')].find(e=>visible(e)&&labels.includes((e.getAttribute('aria-label')||'').trim()));
			if (b) return 'ready';
			const body=document.body?(document.body.innerText||''):'';
			if (/log in|sign in|登录|登入/i.test(body)) return 'logged-out';
			return 'waiting';
		})()`, false, &state)
		if err == nil {
			switch state {
			case "ready":
				return nil
			case "logged-out":
				return fmt.Errorf("网页会话已登出: %w", protocol.ErrSessionExpired)
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(80 * time.Millisecond):
		}
	}
	return errors.New("muse.ai 页面未出现设置入口，额度面板无法打开")
}

func (p *Page) quotaClick(ctx context.Context, expression, errorMessage string) error {
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		var raw string
		if err := p.conn.JS(ctx, expression, false, &raw); err == nil && raw != "" {
			var point struct{ X, Y int }
			if json.Unmarshal([]byte(raw), &point) == nil {
				return p.clickPoint(ctx, point.X, point.Y)
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(80 * time.Millisecond):
		}
	}
	return errors.New(errorMessage)
}

var planPattern = regexp.MustCompile(`(?i)^.{1,60}?(?:\s+plan|計劃|计划|方案|版)$`)
var weeklyResetEN = regexp.MustCompile(`(?i)Weekly limit resets? on ([^\n]+)`)
var weeklyResetZH = regexp.MustCompile(`(?:每(?:週|周|星期)|週|周)(?:上限|限額|限额|額度|额度)(?:會|会|將|将)?(?:於|于|在)\s*([^\n]+?)\s*(?:重設|重置)`)
var extraTokensPattern = regexp.MustCompile(`(?i)^(?:(?:Additional|Extra)\s+tokens|(?:額外|额外)(?:的)?(?:符元|符記|代幣|代币|令牌|憑證|凭证|tokens|使用额度|使用額度|額度|额度))`)
var percentageAfter = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*%\s*(?:used\b|已(?:使)?用)`)
var percentageBefore = regexp.MustCompile(`已(?:使)?用\s*(\d+(?:\.\d+)?)\s*%`)
var parenthesesPattern = regexp.MustCompile(`[\(（]([^\)）]+)[\)）]`)

func percentage(line string) (float64, bool) {
	match := percentageAfter.FindStringSubmatch(line)
	if len(match) == 0 {
		match = percentageBefore.FindStringSubmatch(line)
	}
	if len(match) < 2 {
		return 0, false
	}
	value, err := strconv.ParseFloat(match[1], 64)
	return value, err == nil && value >= 0 && value <= 100
}

func parseQuota(input string) model.Quota {
	input = strings.NewReplacer("\u00a0", " ", "\u3000", " ").Replace(input)
	allLines := strings.Split(strings.ReplaceAll(input, "\r\n", "\n"), "\n")
	lines := make([]string, 0, len(allLines))
	for _, line := range allLines {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	start := 0
	for i, line := range lines {
		if strings.EqualFold(line, "usage") || line == "用量" || line == "使用量" || line == "使用情况" {
			start = i + 1
			break
		}
	}
	lines = lines[start:]
	end := len(lines)
	for i, line := range lines {
		if strings.EqualFold(line, "language") || line == "语言" || line == "語言" {
			end = i
			break
		}
	}
	lines = lines[:end]
	quota := model.Quota{}
	text := strings.Join(lines, "\n")
	for _, line := range lines {
		if planPattern.MatchString(line) {
			quota.Plan = line
			break
		}
	}
	if match := weeklyResetEN.FindStringSubmatch(text); len(match) > 1 {
		quota.WeeklyReset = strings.TrimSpace(match[1])
	} else if match := weeklyResetZH.FindStringSubmatch(text); len(match) > 1 {
		quota.WeeklyReset = strings.TrimSpace(match[1])
	}
	extraAt := len(lines)
	for i, line := range lines {
		if extraTokensPattern.MatchString(line) {
			extraAt = i
			break
		}
	}
	for _, line := range lines[:extraAt] {
		if value, ok := percentage(line); ok {
			quota.WeeklyUsedPct = value
			quota.Found = true
			break
		}
	}
	if extraAt < len(lines) {
		for _, line := range lines[extraAt:] {
			if value, ok := percentage(line); ok {
				quota.ExtraUsedPct = &value
				if match := parenthesesPattern.FindStringSubmatch(line); len(match) > 1 {
					quota.ExtraLeft = strings.TrimSpace(match[1])
				}
				break
			}
		}
	}
	if strings.Contains(strings.ToLower(text), "never expires") || strings.Contains(text, "永不過期") || strings.Contains(text, "永不过期") || strings.Contains(text, "从不过期") || strings.Contains(text, "從不過期") {
		quota.ExtraExpires = "never"
	}
	return quota
}

func (e *Engine) Quota(ctx context.Context, credentials protocol.AccountCredentials) (quota model.Quota, updated protocol.AccountCredentials, returnErr error) {
	e.opMu.Lock()
	defer e.opMu.Unlock()
	updated = cloneCredentials(credentials)
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	page, err := e.authenticatedPageForQuota(ctx, credentials)
	if err != nil {
		return quota, updated, err
	}
	defer page.close()
	defer func() {
		syncCtx, syncCancel := context.WithTimeout(context.Background(), 5*time.Second)
		updated = page.readCredentials(syncCtx, updated)
		syncCancel()
	}()
	page.dismissQuotaUI(ctx)
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 3*time.Second)
		page.dismissQuotaUI(closeCtx)
		closeCancel()
	}()
	if err := page.quotaClick(ctx, quotaSettingsJS, "找不到设置入口（Settings／設定／设置）"); err != nil {
		return quota, updated, err
	}
	if err := page.quotaClick(ctx, quotaMenuJS, "设置菜单未出现可识别的设置项"); err != nil {
		return quota, updated, err
	}
	// Settings opens on General; Usage is a separate in-dialog destination.
	_ = page.quotaClick(ctx, quotaUsageJS, "找不到使用情况入口")
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		var text string
		if err := page.conn.JS(ctx, quotaDialogJS, false, &text); err == nil {
			quota = parseQuota(text)
			if quota.Found {
				quota.CheckedAt = time.Now().Unix()
				return quota, updated, nil
			}
		}
		select {
		case <-ctx.Done():
			return quota, updated, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return quota, updated, errors.New("额度面板未读到有效用量；旧缓存未覆盖")
}
