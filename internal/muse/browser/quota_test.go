package browser

import "testing"

func TestParseQuotaEnglish(t *testing.T) {
	quota := parseQuota("Account\nUsage\nFree plan\nWeekly limit resets on Monday, November 2\n37.5% used\nAdditional tokens\n12.25% used (2,000 left)\nNever expires\nLanguage\nEnglish")
	if !quota.Found || quota.Plan != "Free plan" || quota.WeeklyUsedPct != 37.5 {
		t.Fatalf("unexpected quota: %+v", quota)
	}
	if quota.WeeklyReset != "Monday, November 2" || quota.ExtraUsedPct == nil || *quota.ExtraUsedPct != 12.25 || quota.ExtraLeft != "2,000 left" || quota.ExtraExpires != "never" {
		t.Fatalf("unexpected extra quota: %+v", quota)
	}
}

func TestParseQuotaChinese(t *testing.T) {
	quota := parseQuota("使用量\nPro 计划\n每周额度将于 下周一 重置\n已使用 25 %\n额外代币\n已使用 40%（剩余 600）\n永不过期\n语言")
	if !quota.Found || quota.Plan != "Pro 计划" || quota.WeeklyUsedPct != 25 || quota.WeeklyReset != "下周一" {
		t.Fatalf("unexpected Chinese quota: %+v", quota)
	}
	if quota.ExtraUsedPct == nil || *quota.ExtraUsedPct != 40 || quota.ExtraExpires != "never" {
		t.Fatalf("unexpected Chinese extra quota: %+v", quota)
	}
}

func TestParseQuotaChineseUsagePanel(t *testing.T) {
	quota := parseQuota("设置\n通用\n使用情况\n免费版\n每周限额将在 10月29日重置\n已使用 1%\n额外的使用额度\n从不过期\n已使用 0%（剩余 10 亿个词元）\n升级\n语言\n外观")
	if !quota.Found || quota.Plan != "免费版" || quota.WeeklyUsedPct != 1 || quota.WeeklyReset != "10月29日" {
		t.Fatalf("unexpected usage panel quota: %+v", quota)
	}
	if quota.ExtraUsedPct == nil || *quota.ExtraUsedPct != 0 || quota.ExtraLeft != "剩余 10 亿个词元" || quota.ExtraExpires != "never" {
		t.Fatalf("unexpected usage panel extra quota: %+v", quota)
	}
}

func TestParseQuotaRequiresPercentage(t *testing.T) {
	for _, input := range []string{"", "Usage\nFree plan\nTokens remaining: 100", "Usage\n101% used", "Usage\nLoading..."} {
		if quota := parseQuota(input); quota.Found {
			t.Fatalf("quota without valid percentage marked found: %q => %+v", input, quota)
		}
	}
}
