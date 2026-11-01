package storage

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"muse2api/internal/model"
)

func testKey() []byte { return []byte("01234567890123456789012345678901") }

func openTest(t *testing.T, dir string, key []byte, insecure bool) (*Store, error) {
	t.Helper()
	s, err := Open(dir, key, insecure)
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, nil
}
func TestEncryptedAccountRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := openTest(t, dir, testKey(), false)
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.AddAccount("main", map[string]string{"hatch_sess": "secret", "hatch_vml": "v", "hatch_native_auth_device": "d"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if a.CookieCount != 3 || a.Cookies != nil {
		t.Fatal("credentials leaked from add response")
	}
	s2, err := openTest(t, dir, testKey(), false)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s2.GetAccount(a.ID)
	if err != nil || got.Cookies["hatch_sess"] != "secret" {
		t.Fatalf("round trip failed: %+v %v", got, err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "muse2api.db"))
	if len(b) == 0 || contains(string(b), "secret") {
		t.Fatal("plaintext credential persisted")
	}
	_ = base64.StdEncoding
}
func TestLegacyMigrationBacksUpAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	legacy := `[{"id":"old","label":"old","cookies":{"hatch_sess":"s"},"created_at":1}]`
	if err := os.WriteFile(filepath.Join(dir, "accounts.json"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := openTest(t, dir, testKey(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.ListAccounts()) != 1 {
		t.Fatal("migration failed")
	}
	s2, err := openTest(t, dir, testKey(), false)
	if err != nil || len(s2.ListAccounts()) != 1 {
		t.Fatalf("non-idempotent: %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "backups"))
	if len(entries) != 1 {
		t.Fatal("backup missing")
	}
}
func TestTaskIdempotencyAndRecovery(t *testing.T) {
	dir := t.TempDir()
	s, _ := openTest(t, dir, testKey(), false)
	task, _ := s.CreateTask("image", "p", "p", "key", "hash")
	if _, _, err := s.FindOrCreateTask(model.Task{Kind: "image", Prompt: "p", APIPrompt: "p", IdempotencyKey: "key", RequestHash: "different"}); !errors.Is(err, ErrConflict) {
		t.Fatal("hash conflict not detected")
	}
	existing, created, err := s.FindOrCreateTask(model.Task{Kind: "image", Prompt: "p", APIPrompt: "p", IdempotencyKey: "key", RequestHash: "hash"})
	if err != nil || created || existing.ID != task.ID {
		t.Fatalf("idempotent replay failed: created=%v id=%s err=%v", created, existing.ID, err)
	}
	s2, _ := openTest(t, dir, testKey(), false)
	got, _ := s2.GetTask(task.ID)
	if got.Status != model.TaskQueued {
		t.Fatalf("queued task should survive restart, got %s", got.Status)
	}
}
func TestClearTasksKeepZero(t *testing.T) {
	s, _ := openTest(t, t.TempDir(), testKey(), false)
	_, _ = s.CreateTask("chat", "a", "a", "", "")
	_, _ = s.CreateTask("image", "b", "b", "", "")
	if removed := s.ClearTasks(0); removed != 2 {
		t.Fatalf("removed %d", removed)
	}
	if len(s.ListTasks(0)) != 0 {
		t.Fatal("tasks not cleared")
	}
}
func TestInsecureRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := openTest(t, dir, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.AddAccount("dev", map[string]string{"hatch_sess": "insecure-secret"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := openTest(t, dir, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s2.GetAccount(a.ID)
	if err != nil || got.Cookies["hatch_sess"] != "insecure-secret" {
		t.Fatalf("insecure credentials lost: %+v %v", got, err)
	}
}

func TestAPIKeyPersistence(t *testing.T) {
	dir := t.TempDir()
	s, err := openTest(t, dir, testKey(), false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveAPIKey("m2a_rotated_secret"); err != nil {
		t.Fatal(err)
	}
	s2, err := openTest(t, dir, testKey(), false)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s2.LoadAPIKey()
	if err != nil || got != "m2a_rotated_secret" {
		t.Fatalf("api key round trip failed: %q %v", got, err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "muse2api.db"))
	if contains(string(b), "m2a_rotated_secret") {
		t.Fatal("api key stored in plaintext")
	}
}

func TestMigrationSanitizesPlaintext(t *testing.T) {
	dir := t.TempDir()
	legacy := `[{"id":"old","label":"old","cookies":{"hatch_sess":"PLAINTEXT-SECRET"},"created_at":1}]`
	if err := os.WriteFile(filepath.Join(dir, "accounts.json"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := openTest(t, dir, testKey(), false); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "accounts.json"))
	if contains(string(b), "PLAINTEXT-SECRET") {
		t.Fatal("legacy accounts.json still contains plaintext")
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "backups"))
	for _, entry := range entries {
		b, _ := os.ReadFile(filepath.Join(dir, "backups", entry.Name()))
		if contains(string(b), "PLAINTEXT-SECRET") {
			t.Fatalf("backup %s still contains plaintext", entry.Name())
		}
	}
}

func TestClearTasksPreservesRunning(t *testing.T) {
	s, _ := openTest(t, t.TempDir(), testKey(), false)
	active, _ := s.CreateTask("video", "running", "running", "", "")
	_, _ = s.UpdateTask(active.ID, func(t *model.Task) error { t.Status = model.TaskRunning; return nil })
	done, _ := s.CreateTask("image", "done", "done", "", "")
	_, _ = s.UpdateTask(done.ID, func(t *model.Task) error { t.Status = model.TaskRunning; return nil })
	_, _ = s.UpdateTask(done.ID, func(t *model.Task) error { t.Status = model.TaskSucceeded; return nil })
	if removed := s.ClearTasks(0); removed != 1 {
		t.Fatalf("removed %d", removed)
	}
	if _, err := s.GetTask(active.ID); err != nil {
		t.Fatal("running task was removed")
	}
	if _, err := s.GetTask(done.ID); err == nil {
		t.Fatal("terminal task was not removed")
	}
}

func TestClaimTaskIsAtomic(t *testing.T) {
	s, err := openTest(t, t.TempDir(), testKey(), false)
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateTask("image", "p", "p", "", "")
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if claimed.Status != model.TaskRunning || claimed.StartedAt == 0 || claimed.Progress != 5 {
		t.Fatalf("unexpected claimed task: %+v", claimed)
	}
	if _, err := s.ClaimTask(task.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("second claim should conflict, got %v", err)
	}
	if _, err := s.ClaimTask("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing task error = %v", err)
	}
}

func TestClaimTaskClearsQueuedMessage(t *testing.T) {
	s, err := openTest(t, t.TempDir(), testKey(), false)
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateTask("image", "p", "p", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateTask(task.ID, func(t *model.Task) error {
		t.Status = model.TaskQueued
		t.Error = "服务关闭前尚未分配账号，已重新排队"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if claimed.Error != "" {
		t.Fatalf("stale queue message survived claim: %q", claimed.Error)
	}
}

func TestListAccountsDeepCopiesPublicPointers(t *testing.T) {
	s, err := openTest(t, t.TempDir(), testKey(), false)
	if err != nil {
		t.Fatal(err)
	}
	account, err := s.AddAccount("a", map[string]string{"hatch_sess": "s", "hatch_vml": "v", "hatch_native_auth_device": "d"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	used := 12.5
	ok := true
	if _, err := s.UpdateAccount(account.ID, func(a *model.Account) error {
		a.OK = &ok
		a.Quota = &model.Quota{Found: true, ExtraUsedPct: &used}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	listed := s.ListAccounts()[0]
	*listed.OK = false
	*listed.Quota.ExtraUsedPct = 99
	got, err := s.GetAccount(account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.OK == nil || !*got.OK || got.Quota == nil || got.Quota.ExtraUsedPct == nil || *got.Quota.ExtraUsedPct != used {
		t.Fatalf("list leaked shared pointers: %+v", got)
	}
}

func TestListTasksLimitAndCounts(t *testing.T) {
	s, err := openTest(t, t.TempDir(), testKey(), false)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, 3)
	for index := 0; index < 3; index++ {
		task, err := s.CreateTask("image", "p", "p", "", "")
		if err != nil {
			t.Fatal(err)
		}
		created := int64(index + 1)
		if _, err := s.UpdateTask(task.ID, func(current *model.Task) error {
			current.CreatedAt = created
			current.Result = map[string]any{"nested": map[string]any{"value": index}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, task.ID)
	}
	recent := s.ListTasks(2)
	if len(recent) != 2 || recent[0].ID != ids[2] || recent[1].ID != ids[1] {
		t.Fatalf("unexpected recent tasks: %+v", recent)
	}
	// Returned result maps must not alias store state.
	recent[0].Result["nested"].(map[string]any)["value"] = 99
	stored, err := s.GetTask(ids[2])
	if err != nil {
		t.Fatal(err)
	}
	if value, ok := stored.Result["nested"].(map[string]any)["value"].(float64); !ok || value != 2 {
		t.Fatalf("result map aliased store: %+v", stored.Result)
	}
	counts := s.TaskCounts(2)
	if counts.ImageTotal != 3 || counts.RequestsToday != 2 || counts.Queued != 3 {
		t.Fatalf("unexpected counts: %+v", counts)
	}
}

func TestSafeFilename(t *testing.T) {
	for _, name := range []string{"../x", "a/b", "a\\b", ".."} {
		if SafeFilename(name) {
			t.Fatalf("unsafe %q", name)
		}
	}
	if !SafeFilename("image.png") {
		t.Fatal("safe name rejected")
	}
}
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestEssentialCookiesDoNotRequireDeviceCookie(t *testing.T) {
	s, err := openTest(t, t.TempDir(), testKey(), false)
	if err != nil {
		t.Fatal(err)
	}
	two, err := s.AddAccount("two", map[string]string{"hatch_sess": "s", "hatch_vml": "v"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !two.EssentialOK {
		t.Fatal("sess + vml should be enough")
	}
	one, err := s.AddAccount("one", map[string]string{"hatch_sess": "s"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if one.EssentialOK {
		t.Fatal("sess without vml should not be essential-complete")
	}
}

func TestBackupAndStagedRestore(t *testing.T) {
	dir := t.TempDir()
	s, err := openTest(t, dir, testKey(), false)
	if err != nil {
		t.Fatal(err)
	}
	account, err := s.AddAccount("before", map[string]string{"hatch_sess": "s", "hatch_vml": "v"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	backup, err := s.Backup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateAccount(account.ID, func(a *model.Account) error { a.Label = "after"; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := s.StageRestore(context.Background(), backup); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(dir, testKey(), false)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	got, err := restored.GetAccount(account.ID)
	if err != nil || got.Label != "before" {
		t.Fatalf("restore did not apply: %+v %v", got, err)
	}
}

func TestAccountExpiryIsNotEstimatedFromCreatedAt(t *testing.T) {
	dir := t.TempDir()
	s, err := openTest(t, dir, testKey(), false)
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.AddAccount("old", map[string]string{"hatch_sess": "s", "hatch_vml": "v"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if a.ExpiresAt != 0 || a.ExpiryEstimated {
		t.Fatalf("unexpected estimated expiry: %+v", a)
	}
	if _, err := s.UpdateAccount(a.ID, func(account *model.Account) error {
		account.CreatedAt = time.Now().Add(-30 * 24 * time.Hour).Unix()
		account.Cookies = map[string]string{"hatch_sess": "s2", "hatch_vml": "v2"}
		account.ExpiresAt = 0
		account.ExpiryEstimated = false
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetAccount(a.ID)
	if err != nil || got.ExpiresAt != 0 {
		t.Fatalf("credential update reintroduced expiry: %+v %v", got, err)
	}
}

func TestTaskReferencesSurviveClaim(t *testing.T) {
	s, err := openTest(t, t.TempDir(), testKey(), false)
	if err != nil {
		t.Fatal(err)
	}
	task, _, err := s.FindOrCreateTask(model.Task{Kind: "video", Prompt: "p", APIPrompt: "p", ReferenceImages: []string{"data:image/png;base64,AAA"}})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed.ReferenceImages) != 1 {
		t.Fatalf("references lost across claim: %+v", claimed.ReferenceImages)
	}
}

func TestSecureJSONMigration(t *testing.T) {
	dir := t.TempDir()
	box, err := newSealBox(testKey())
	if err != nil {
		t.Fatal(err)
	}
	payload, err := box.Seal([]byte(`{"a":{"cookies":{"hatch_sess":"s","hatch_vml":"v"},"cookies_exp":{}}}`))
	if err != nil {
		t.Fatal(err)
	}
	disk := diskAccounts{
		Version:   1,
		Encrypted: true,
		Payload:   payload,
		Accounts:  []model.Account{{ID: "a", Label: "legacy", Enabled: true, CreatedAt: 1}},
	}
	data, _ := json.Marshal(disk)
	if err := os.WriteFile(filepath.Join(dir, "accounts.secure.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := openTest(t, dir, testKey(), false)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetAccount("a")
	if err != nil || got.Cookies["hatch_sess"] != "s" {
		t.Fatalf("secure migration failed: %+v %v", got, err)
	}
}

func TestRequeueTaskGuardrails(t *testing.T) {
	s, err := openTest(t, t.TempDir(), testKey(), false)
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateTask("image", "p", "p", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateTask(task.ID, func(t *model.Task) error { t.Status = model.TaskFailed; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RequeueTask(task.ID, false); err != nil {
		t.Fatalf("failed task should requeue: %v", err)
	}
	if _, err := s.ClaimTask(task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateTask(task.ID, func(t *model.Task) error { t.Status = model.TaskUnknown; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RequeueTask(task.ID, false); !errors.Is(err, ErrConflict) {
		t.Fatalf("unknown task without force should conflict: %v", err)
	}
	if _, err := s.RequeueTask(task.ID, true); err != nil {
		t.Fatalf("forced requeue failed: %v", err)
	}
}

func TestStoreScaleTenAccountsThousandTasks(t *testing.T) {
	s, err := openTest(t, t.TempDir(), testKey(), false)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if _, err := s.AddAccount(fmt.Sprintf("a%d", i), map[string]string{"hatch_sess": "s", "hatch_vml": "v"}, nil); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 1000; i++ {
		if _, err := s.CreateTask("image", "prompt", "prompt", "", ""); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(s.ListTasks(100)); got != 100 {
		t.Fatalf("list returned %d", got)
	}
	counts := s.TaskCounts(0)
	if counts.ImageTotal != 1000 || counts.Queued != 1000 {
		t.Fatalf("unexpected counts: %+v", counts)
	}
}
