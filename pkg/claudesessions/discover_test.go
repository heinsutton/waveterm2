// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package claudesessions

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const (
	idNamed   = "11111111-1111-4111-8111-111111111111"
	idUnnamed = "22222222-2222-4222-8222-222222222222"
	idLive    = "33333333-3333-4333-8333-333333333333"
	idHuge    = "44444444-4444-4444-8444-444444444444"
	idSdk     = "55555555-5555-4555-8555-555555555555"
)

func write(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fixture(t *testing.T) *Provider {
	dir := t.TempDir()
	proj := filepath.Join(dir, "projects", "-home-x-proj")
	write(t, filepath.Join(proj, idNamed+".jsonl"),
		`{"type":"custom-title","customTitle":"old name","sessionId":"x"}`+"\n"+
			`{"type":"user","cwd":"/home/x/proj","message":"hi"}`+"\n"+
			`not json at all`+"\n"+
			`{"type":"custom-title","customTitle":"baldr","sessionId":"x"}`+"\n"+
			`{"type":"last-prompt","lastPrompt":"do the\nthing"}`+"\n")
	write(t, filepath.Join(proj, idUnnamed+".jsonl"), `{"type":"mode","mode":"normal"}`+"\n")
	write(t, filepath.Join(proj, idLive+".jsonl"), `{"type":"user","cwd":"/home/x/proj"}`+"\n")
	// Past both read windows: the name sits in the head, the last prompt in the tail.
	huge := `{"type":"custom-title","customTitle":"big","sessionId":"x"}` + "\n" +
		`{"type":"user","cwd":"/home/x/big"}` + "\n" +
		strings.Repeat(`{"type":"assistant","message":"`+strings.Repeat("a", 900)+`"}`+"\n", 400) +
		`{"type":"last-prompt","lastPrompt":"the end"}` + "\n"
	write(t, filepath.Join(proj, idHuge+".jsonl"), huge)
	write(t, filepath.Join(dir, "sessions", "100.json"),
		fmt.Sprintf(`{"pid":100,"sessionId":%q,"cwd":"/home/x/proj","kind":"interactive","name":"live one","status":"busy","updatedAt":%d}`, idLive, time.Now().UnixMilli()))
	write(t, filepath.Join(dir, "sessions", "101.json"), fmt.Sprintf(`{"pid":101,"sessionId":%q,"kind":"interactive"}`, idNamed))
	write(t, filepath.Join(dir, "sessions", "102.json"), fmt.Sprintf(`{"pid":102,"sessionId":%q,"kind":"sdk"}`, idSdk))
	write(t, filepath.Join(dir, "sessions", "103.json"), `{broken`)
	write(t, filepath.Join(dir, "sessions", "100.abc.key"), `secret`)
	write(t, filepath.Join(dir, "history.jsonl"),
		fmt.Sprintf(`{"display":"from history","timestamp":5,"project":"/home/x/proj","sessionId":%q}`, idUnnamed)+"\n"+"garbage\n")
	p := MakeProvider(dir)
	p.pidAlive = func(pid int) bool { return pid == 100 }
	p.blockOf = func(pid int) string { return "block-" + fmt.Sprint(pid) }
	return p
}

func byId(ss []ClaudeSession) map[string]ClaudeSession {
	m := map[string]ClaudeSession{}
	for _, s := range ss {
		m[s.SessionId] = s
	}
	return m
}

func TestDiscover(t *testing.T) {
	sessions := fixture(t).Discover()
	m := byId(sessions)
	if len(sessions) != 4 {
		t.Fatalf("want 4 sessions, got %d", len(sessions))
	}
	if s := m[idNamed]; s.Name != "baldr" || s.Cwd != "/home/x/proj" || s.Preview != "do the thing" || s.Pid != 0 {
		t.Errorf("named: %+v", s)
	}
	if s := m[idUnnamed]; s.Name != "" || s.Cwd != "/home/x/proj" || s.Preview != "from history" {
		t.Errorf("unnamed should fall back to history: %+v", s)
	}
	if s := m[idLive]; s.Name != "live one" || s.Pid != 100 || s.Status != "busy" {
		t.Errorf("live: %+v", s)
	}
	if s := m[idHuge]; s.Name != "big" || s.Cwd != "/home/x/big" || s.Preview != "the end" {
		t.Errorf("huge: %+v", s)
	}
	if _, ok := m[idSdk]; ok {
		t.Errorf("sdk session must be skipped")
	}
}

func TestDiscoverEmptyDir(t *testing.T) {
	p := MakeProvider(filepath.Join(t.TempDir(), "missing"))
	if got := p.Discover(); len(got) != 0 {
		t.Errorf("want none, got %d", len(got))
	}
}

func TestDiscoverLiveWithoutTranscript(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "sessions", "7.json"), fmt.Sprintf(`{"pid":7,"sessionId":%q,"cwd":"/a","kind":"interactive","status":"idle"}`, idLive))
	p := MakeProvider(dir)
	p.pidAlive = func(int) bool { return true }
	p.blockOf = func(int) string { return "" }
	got := p.Discover()
	if len(got) != 1 || got[0].Cwd != "/a" || got[0].Pid != 7 || !got[0].External || got[0].BlockId != "" {
		t.Errorf("got %+v", got)
	}
}

func TestCacheReusedUntilFileChanges(t *testing.T) {
	p := fixture(t)
	p.Discover()
	path := filepath.Join(p.claudeDir, "projects", "-home-x-proj", idNamed+".jsonl")
	fi, _ := os.Stat(path)
	p.lock.Lock()
	p.cache[path] = cacheEntry{key: cacheKey{mtime: fi.ModTime().UnixMilli(), size: fi.Size()}, info: transcriptInfo{Name: "cached"}}
	p.lock.Unlock()
	if s := byId(p.Discover())[idNamed]; s.Name != "cached" {
		t.Errorf("unchanged file should come from cache, got %q", s.Name)
	}
	write(t, path, `{"type":"custom-title","customTitle":"fresh"}`+"\n")
	if s := byId(p.Discover())[idNamed]; s.Name != "fresh" {
		t.Errorf("changed file should be re-read, got %q", s.Name)
	}
}

func TestListStore(t *testing.T) {
	cfg := t.TempDir()
	write(t, filepath.Join(cfg, StoreFileName), `{"folders":[{"path":"/a"}],"descriptions":{"`+idNamed+`":"notes"}}`)
	res := List([]Harness{fixture(t)}, cfg)
	if len(res.Folders) != 1 || res.Descriptions[idNamed] != "notes" {
		t.Errorf("store not loaded: %+v", res)
	}
	bad := t.TempDir()
	write(t, filepath.Join(bad, StoreFileName), `{broken`)
	res = List([]Harness{fixture(t)}, bad)
	if res.Folders == nil || res.Descriptions == nil {
		t.Errorf("damaged store must give empty, non-nil values")
	}
}

func TestIsSessionId(t *testing.T) {
	if !IsSessionId(idNamed) || IsSessionId("../etc") || IsSessionId(idNamed+";rm") {
		t.Errorf("session id check wrong")
	}
}

func TestDiscoverStates(t *testing.T) {
	m := byId(fixture(t).Discover())
	if m[idLive].State != StateBusy || m[idNamed].State != StateOffline {
		t.Errorf("states: live=%q named=%q", m[idLive].State, m[idNamed].State)
	}
}

func TestApplyBlockStates(t *testing.T) {
	mk := func(id, state, status string, statusTs int64) ClaudeSession {
		return ClaudeSession{SessionId: id, State: state, Status: status, StatusTs: statusTs}
	}
	sessions := []ClaudeSession{
		mk("a", StateIdle, StateIdle, 100),
		mk("b", StateOffline, "", 0),
		mk("c", StateIdle, StateIdle, 500),
		mk("d", StateBusy, StateBusy, 100),
		mk("e", StateIdle, StateIdle, 100),
	}
	ApplyBlockStates(sessions, []BlockClaude{
		{BlockId: "b1", SessionId: "a", State: StateWaiting, Ts: 200},
		{BlockId: "b2", SessionId: "b", State: StateWaiting, Ts: 200},
		{BlockId: "b3", SessionId: "c", State: StateWaiting, Ts: 200},
		{BlockId: "b4", SessionId: "d", State: StateIdle, Ts: 200},
		{BlockId: "old", SessionId: "e", State: StateBusy, Ts: 50},
		{BlockId: "new", SessionId: "e", State: StateWaiting, Ts: 150},
		{BlockId: "bad", SessionId: "x", State: "bogus", Ts: 1},
	})
	want := map[string][2]string{
		"a": {StateWaiting, "b1"},
		"b": {StateOffline, ""},
		"c": {StateIdle, "b3"},
		"d": {StateIdle, "b4"},
		"e": {StateWaiting, "new"},
	}
	for _, s := range sessions {
		if w := want[s.SessionId]; s.State != w[0] || s.BlockId != w[1] {
			t.Errorf("%s: got state=%q block=%q, want %v", s.SessionId, s.State, s.BlockId, w)
		}
	}
}

func TestRenameDoesNotMoveLastActive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "projects", "-a", idNamed+".jsonl")
	write(t, path, `{"type":"user","cwd":"/a","timestamp":"2026-10-10T10:00:00.000Z"}`+"\n"+
		`{"type":"assistant","timestamp":"2026-10-10T10:05:00.000Z"}`+"\n"+
		`{"type":"custom-title","customTitle":"renamed"}`+"\n")
	got := MakeProvider(dir).Discover()
	want := time.Date(2026, 10, 10, 10, 5, 0, 0, time.UTC).UnixMilli()
	if len(got) != 1 || got[0].LastActive != want || got[0].Name != "renamed" {
		t.Errorf("last active should be the last message, got %+v (want %d)", got, want)
	}
}

func TestBlockOfPidReadsChildEnvironment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a unix sleep binary")
	}
	child := exec.Command("sleep", "5")
	child.Env = append(os.Environ(), "WAVETERM_BLOCKID=abc-123")
	if err := child.Start(); err != nil {
		t.Skipf("cannot start sleep: %v", err)
	}
	defer func() {
		_ = child.Process.Kill()
		_ = child.Wait()
	}()
	if got := blockOfPid(child.Process.Pid); got != "abc-123" {
		t.Errorf("got %q", got)
	}
	plain := exec.Command("sleep", "5")
	plain.Env = []string{"PATH=" + os.Getenv("PATH")}
	if err := plain.Start(); err != nil {
		t.Skipf("cannot start sleep: %v", err)
	}
	defer func() {
		_ = plain.Process.Kill()
		_ = plain.Wait()
	}()
	if got := blockOfPid(plain.Process.Pid); got != "" {
		t.Errorf("a process outside Bifrost should have no block, got %q", got)
	}
}

func TestPrepareResume(t *testing.T) {
	p := fixture(t)
	real := t.TempDir()
	id := "66666666-6666-4666-8666-666666666666"
	write(t, filepath.Join(p.claudeDir, "projects", "-r", id+".jsonl"), `{"type":"user","cwd":"`+real+`","timestamp":"2026-10-10T10:00:00.000Z"}`+"\n")
	p.lookPath = func(string) (string, error) { return "/usr/bin/claude", nil }

	got, err := p.PrepareResume(id)
	if err != nil || got.Cmd != "/usr/bin/claude" || got.Cwd != real || len(got.Args) != 2 || got.Args[0] != "-r" || got.Args[1] != id {
		t.Errorf("resume: %+v %v", got, err)
	}
	if _, err := p.PrepareResume(idLive); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Errorf("live session must be refused, got %v", err)
	}
	if _, err := p.PrepareResume("../etc/passwd"); err == nil {
		t.Errorf("non-uuid id must be refused")
	}
	if _, err := p.PrepareResume("77777777-7777-4777-8777-777777777777"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("unknown session must be refused, got %v", err)
	}
	if _, err := p.PrepareResume(idNamed); err == nil || !strings.Contains(err.Error(), "folder not found") {
		t.Errorf("missing folder must be refused, got %v", err)
	}
	p.lookPath = func(string) (string, error) { return "", os.ErrNotExist }
	if _, err := p.PrepareResume(id); err == nil || !strings.Contains(err.Error(), "PATH") {
		t.Errorf("missing claude binary must be reported, got %v", err)
	}
}

func TestPrepareNew(t *testing.T) {
	p := fixture(t)
	p.lookPath = func(string) (string, error) { return "/usr/bin/claude", nil }
	dir := t.TempDir()
	got, err := p.PrepareNew(dir)
	if err != nil || got.Cwd != dir || len(got.Args) != 0 {
		t.Errorf("new: %+v %v", got, err)
	}
	for _, bad := range []string{"", "relative/dir", filepath.Join(dir, "missing")} {
		if _, err := p.PrepareNew(bad); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
}

func TestFolderStore(t *testing.T) {
	cfg := t.TempDir()
	a, b := t.TempDir(), t.TempDir()
	if _, err := AddFolder(cfg, a, "alpha"); err != nil {
		t.Fatal(err)
	}
	if _, err := AddFolder(cfg, b, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := AddFolder(cfg, a+"/", "renamed"); err != nil {
		t.Fatal(err)
	}
	sd := loadStore(cfg)
	if len(sd.Folders) != 2 || sd.Folders[0].Label != "renamed" {
		t.Errorf("add/dedupe: %+v", sd.Folders)
	}
	if _, err := AddFolder(cfg, filepath.Join(a, "missing"), ""); err == nil {
		t.Errorf("missing folder must be refused")
	}
	if _, err := AddFolder(cfg, "rel", ""); err == nil {
		t.Errorf("relative folder must be refused")
	}
	write(t, filepath.Join(cfg, StoreFileName), `{"folders":[{"path":"`+a+`"}],"descriptions":{"`+idNamed+`":"keep me"}}`)
	if _, err := AddFolder(cfg, b, ""); err != nil {
		t.Fatal(err)
	}
	if sd := loadStore(cfg); sd.Descriptions[idNamed] != "keep me" || len(sd.Folders) != 2 {
		t.Errorf("descriptions must survive a write: %+v", sd)
	}
	if err := RemoveFolder(cfg, a); err != nil {
		t.Fatal(err)
	}
	if err := RemoveFolder(cfg, a); err == nil {
		t.Errorf("removing an unknown folder must fail")
	}
	if sd := loadStore(cfg); len(sd.Folders) != 1 || sd.Folders[0].Path != b {
		t.Errorf("remove: %+v", sd.Folders)
	}
	if _, err := os.Stat(a); err != nil {
		t.Errorf("removing a remembered folder must not delete it")
	}
}

func TestListMissing(t *testing.T) {
	cfg := t.TempDir()
	res := List([]Harness{fixture(t)}, cfg)
	found := false
	for _, m := range res.Missing {
		if m == "/home/x/proj" {
			found = true
		}
	}
	if !found {
		t.Errorf("missing folders not reported: %v", res.Missing)
	}
}

func TestRecentPrompts(t *testing.T) {
	dir := t.TempDir()
	line := func(text string, ts int, id string) string {
		return fmt.Sprintf(`{"display":%q,"timestamp":%d,"project":"/a","sessionId":%q}`, text, ts, id) + "\n"
	}
	write(t, filepath.Join(dir, "history.jsonl"),
		line("first", 1, idNamed)+line("/exit", 2, idNamed)+line("other session", 3, idLive)+
			line("third\nwith newline", 5, idNamed)+"garbage\n"+line("second", 4, idNamed)+line("   ", 6, idNamed))
	p := MakeProvider(dir)
	got, err := p.RecentPrompts(idNamed, 2)
	if err != nil || len(got) != 2 || got[0].Text != "third with newline" || got[1].Text != "second" {
		t.Errorf("newest first, noise dropped, limited: %+v %v", got, err)
	}
	if got, _ := p.RecentPrompts(idNamed, 10); len(got) != 3 {
		t.Errorf("want 3 real prompts, got %+v", got)
	}
	if _, err := p.RecentPrompts("nope", 5); err == nil {
		t.Errorf("non-uuid id must be refused")
	}
	if got, err := MakeProvider(t.TempDir()).RecentPrompts(idNamed, 5); err != nil || len(got) != 0 {
		t.Errorf("missing history must give none: %+v %v", got, err)
	}
}

func TestSetDescription(t *testing.T) {
	cfg := t.TempDir()
	if err := SetDescription(cfg, idNamed, "  working on\nthe parser  "); err != nil {
		t.Fatal(err)
	}
	if d := loadStore(cfg).Descriptions[idNamed]; d != "working on the parser" {
		t.Errorf("description not cleaned: %q", d)
	}
	a := t.TempDir()
	if _, err := AddFolder(cfg, a, ""); err != nil {
		t.Fatal(err)
	}
	if sd := loadStore(cfg); len(sd.Folders) != 1 || sd.Descriptions[idNamed] == "" {
		t.Errorf("folders and descriptions must coexist: %+v", sd)
	}
	if err := SetDescription(cfg, idNamed, "   "); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadStore(cfg).Descriptions[idNamed]; ok {
		t.Errorf("empty description must remove it")
	}
	if err := SetDescription(cfg, "../x", "hi"); err == nil {
		t.Errorf("non-uuid id must be refused")
	}
	long := strings.Repeat("x", 900)
	_ = SetDescription(cfg, idNamed, long)
	if d := loadStore(cfg).Descriptions[idNamed]; len([]rune(d)) > maxDescriptionLen+1 {
		t.Errorf("description too long: %d", len([]rune(d)))
	}
}

func TestSetHidden(t *testing.T) {
	cfg := t.TempDir()
	if err := SetHidden(cfg, idNamed, true); err != nil {
		t.Fatal(err)
	}
	if err := SetHidden(cfg, idNamed, true); err != nil {
		t.Fatal(err)
	}
	if err := SetDescription(cfg, idLive, "kept"); err != nil {
		t.Fatal(err)
	}
	res := List([]Harness{fixture(t)}, cfg)
	hidden := 0
	for _, s := range res.Sessions {
		if s.Hidden {
			hidden++
			if s.SessionId != idNamed {
				t.Errorf("wrong session hidden: %s", s.SessionId)
			}
		}
	}
	if hidden != 1 || len(loadStore(cfg).Hidden) != 1 || loadStore(cfg).Descriptions[idLive] != "kept" {
		t.Errorf("hide twice must store once and keep other data: %+v", loadStore(cfg))
	}
	if err := SetHidden(cfg, idNamed, false); err != nil {
		t.Fatal(err)
	}
	if len(loadStore(cfg).Hidden) != 0 {
		t.Errorf("unhide must remove it")
	}
	if err := SetHidden(cfg, "../x", true); err == nil {
		t.Errorf("non-uuid id must be refused")
	}
}

// fakeHarness is a second harness used to prove merging and per-harness dispatch.
type fakeHarness struct {
	name     string
	sessions []ClaudeSession
	resumed  []string
}

func (f *fakeHarness) Name() string              { return f.name }
func (f *fakeHarness) Discover() []ClaudeSession { return f.sessions }
func (f *fakeHarness) PrepareNew(cwd string) (*ClaudeLaunch, error) {
	return &ClaudeLaunch{Cmd: f.name, Cwd: cwd}, nil
}
func (f *fakeHarness) PrepareResume(id string) (*ClaudeLaunch, error) {
	f.resumed = append(f.resumed, id)
	return &ClaudeLaunch{Cmd: f.name, Args: []string{"--conversation", id}}, nil
}
func (f *fakeHarness) RecentPrompts(id string, limit int) ([]ClaudePrompt, error) {
	return []ClaudePrompt{{Ts: 1, Text: f.name}}, nil
}

func TestListMergesHarnessesNewestFirst(t *testing.T) {
	a := &fakeHarness{name: "a", sessions: []ClaudeSession{{Harness: "a", SessionId: "a1", LastActive: 100}, {Harness: "a", SessionId: "a2", LastActive: 10}}}
	b := &fakeHarness{name: "b", sessions: []ClaudeSession{{Harness: "b", SessionId: "b1", LastActive: 50}}}
	res := List([]Harness{a, b}, t.TempDir())
	var ids []string
	for _, s := range res.Sessions {
		ids = append(ids, s.SessionId)
	}
	if got := strings.Join(ids, ","); got != "a1,b1,a2" {
		t.Fatalf("order = %s", got)
	}
}

func TestFindHarnessDispatch(t *testing.T) {
	a := &fakeHarness{name: HarnessClaude}
	b := &fakeHarness{name: "b"}
	hs := []Harness{a, b}
	if h, err := FindHarness(hs, ""); err != nil || h != Harness(a) {
		t.Fatalf("empty name must pick claude, got %v %v", h, err)
	}
	h, err := FindHarness(hs, "b")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.PrepareResume("x"); err != nil {
		t.Fatal(err)
	}
	if len(a.resumed) != 0 || len(b.resumed) != 1 {
		t.Fatalf("resume went to the wrong harness: a=%v b=%v", a.resumed, b.resumed)
	}
	if _, err := FindHarness(hs, "nope"); err == nil {
		t.Fatal("unknown harness must error")
	}
}
