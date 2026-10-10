// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package agy

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cs "github.com/wavetermdev/waveterm/pkg/claudesessions"
)

const zero = "0001-01-01 00:00:00+00:00"

const (
	idNamed   = "11111111-1111-4111-8111-111111111111"
	idUntitle = "22222222-2222-4222-8222-222222222222"
	idSub     = "33333333-3333-4333-8333-333333333333"
	idKilled  = "44444444-4444-4444-8444-444444444444"
	idEmpty   = "55555555-5555-4555-8555-555555555555"
	idBadURI  = "66666666-6666-4666-8666-666666666666"
	idMulti   = "77777777-7777-4777-8777-777777777777"
)

type row struct {
	id, title, preview, modified, input, uris, parent, status string
	steps, depth                                              int
	killed                                                    bool
}

func makeDir(t *testing.T, rows []row, history string) string {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite3", "file:"+filepath.Join(dir, summariesDBName))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE conversation_summaries (
		conversation_id TEXT PRIMARY KEY, title TEXT NOT NULL DEFAULT "", preview TEXT NOT NULL DEFAULT "",
		step_count INTEGER NOT NULL DEFAULT 0, last_modified_time datetime NOT NULL, workspace_uris TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT "", parent_conversation_id TEXT NOT NULL DEFAULT "",
		nesting_depth INTEGER NOT NULL DEFAULT 0, killed numeric NOT NULL DEFAULT false,
		last_user_input_time datetime NOT NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if _, err := db.Exec(`INSERT INTO conversation_summaries (conversation_id,title,preview,step_count,last_modified_time,workspace_uris,parent_conversation_id,nesting_depth,killed,last_user_input_time,status) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
			r.id, r.title, r.preview, r.steps, r.modified, r.uris, r.parent, r.depth, r.killed, r.input, r.status); err != nil {
			t.Fatal(err)
		}
	}
	if history != "" {
		if err := os.WriteFile(filepath.Join(dir, "history.jsonl"), []byte(history), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func fixtureRows() []row {
	return []row{
		{id: idNamed, title: "Hook Probe", preview: "Hook Probe", steps: 77, modified: "2026-10-10 19:17:05.180673087+00:00", input: "2026-10-10 19:16:37.057014234+00:00", uris: `["file:///home/me/my%20repo"]`},
		{id: idUntitle, preview: "Vault setup", steps: 5, modified: "2026-10-09 10:00:00+00:00", input: zero, uris: `["file:///home/me","file:///other"]`},
		{id: idSub, title: "sub", steps: 3, modified: "2026-10-10 10:00:00+00:00", input: zero, uris: `["file:///home/me"]`, parent: idNamed, depth: 1},
		{id: idKilled, title: "dead", steps: 3, modified: "2026-10-10 10:00:00+00:00", input: zero, uris: `["file:///home/me"]`, killed: true},
		{id: idEmpty, modified: zero, input: zero, uris: `["file:///home/me"]`},
		{id: idBadURI, title: "bad", steps: 1, modified: "2026-10-08 10:00:00+00:00", input: zero, uris: `not json`},
	}
}

func TestDiscoverMapsRows(t *testing.T) {
	dir := makeDir(t, fixtureRows(), "")
	got := MakeProvider(dir).Discover()
	byId := map[string]cs.ClaudeSession{}
	for _, s := range got {
		byId[s.SessionId] = s
	}
	if len(got) != 3 {
		t.Fatalf("want 3 sessions (named, untitled, bad uri), got %d: %+v", len(got), got)
	}
	n := byId[idNamed]
	if n.Name != "Hook Probe" || n.Cwd != "/home/me/my repo" || n.Harness != "agy" || n.State != cs.StateOffline {
		t.Fatalf("named: %+v", n)
	}
	if n.LastActive != 1791659797057 { // last_user_input_time wins over last_modified_time
		t.Fatalf("named last active = %d", n.LastActive)
	}
	u := byId[idUntitle]
	if u.Name != "" || u.Cwd != "/home/me" || u.Preview != "Vault setup" || u.LastActive == 0 {
		t.Fatalf("untitled (first workspace, modified time as fallback): %+v", u)
	}
	if b := byId[idBadURI]; b.Cwd != "" {
		t.Fatalf("bad uri must give an empty cwd: %+v", b)
	}
	if got[0].SessionId != idNamed {
		t.Fatalf("newest first, got %s", got[0].SessionId)
	}
}

func TestDiscoverUsesHistoryForPreviewAndTime(t *testing.T) {
	hist := `{"display":"first","timestamp":1,"conversationId":"` + idUntitle + `"}
{"display":"/config","timestamp":9999999999999,"conversationId":"` + idUntitle + `","type":"slash_command"}
{"display":"latest prompt","timestamp":1790000000000,"conversationId":"` + idUntitle + `"}
not json
`
	dir := makeDir(t, fixtureRows(), hist)
	for _, s := range MakeProvider(dir).Discover() {
		if s.SessionId == idUntitle && s.Preview != "latest prompt" {
			t.Fatalf("preview = %q", s.Preview)
		}
	}
}

func TestDiscoverMissingOrDamagedDB(t *testing.T) {
	if got := MakeProvider(t.TempDir()).Discover(); len(got) != 0 {
		t.Fatalf("missing db: %+v", got)
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, summariesDBName), []byte("this is not sqlite"), 0o644)
	if got := MakeProvider(dir).Discover(); len(got) != 0 {
		t.Fatalf("damaged db: %+v", got)
	}
}

func TestDiscoverToleratesMissingColumns(t *testing.T) {
	dir := t.TempDir()
	db, _ := sql.Open("sqlite3", "file:"+filepath.Join(dir, summariesDBName))
	defer db.Close()
	db.Exec(`CREATE TABLE conversation_summaries (conversation_id TEXT PRIMARY KEY, title TEXT, last_modified_time datetime, workspace_uris TEXT)`)
	db.Exec(`INSERT INTO conversation_summaries VALUES (?,?,?,?)`, idNamed, "Only some columns", "2026-10-10 10:00:00+00:00", `["file:///home/me"]`)
	got := MakeProvider(dir).Discover()
	if len(got) != 1 || got[0].Name != "Only some columns" || got[0].Cwd != "/home/me" {
		t.Fatalf("got %+v", got)
	}
}

func TestDiscoverReloadsWhenDBChanges(t *testing.T) {
	dir := makeDir(t, fixtureRows()[:1], "")
	p := MakeProvider(dir)
	if len(p.Discover()) != 1 {
		t.Fatal("first read")
	}
	db, _ := sql.Open("sqlite3", "file:"+filepath.Join(dir, summariesDBName))
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO conversation_summaries (conversation_id,title,step_count,last_modified_time,workspace_uris,last_user_input_time) VALUES (?,?,?,?,?,?)`,
		idUntitle, "second", 2, "2026-10-10 20:00:00+00:00", `["file:///x"]`, zero); err != nil {
		t.Fatal(err)
	}
	if len(p.Discover()) != 2 {
		t.Fatal("cache must refresh when the database file changes")
	}
}

func liveProvider(dir string, live map[string]liveProc, blocks map[int]string) *Provider {
	p := MakeProvider(dir)
	p.findLive = func() map[string]liveProc { return live }
	p.blockOf = func(pid int) string { return blocks[pid] }
	return p
}

func TestLiveStates(t *testing.T) {
	rows := fixtureRows()[:2]
	rows[0].status = "CASCADE_RUN_STATUS_RUNNING"
	rows[1].status = "CASCADE_RUN_STATUS_IDLE"
	dir := makeDir(t, rows, "")
	live := map[string]liveProc{idNamed: {Pid: 10}, idUntitle: {Pid: 20}}
	got := map[string]cs.ClaudeSession{}
	for _, s := range liveProvider(dir, live, map[int]string{10: "block-a"}).Discover() {
		got[s.SessionId] = s
	}
	if s := got[idNamed]; s.State != cs.StateBusy || s.BlockId != "block-a" || s.External || s.Pid != 10 {
		t.Fatalf("running in a Bifrost pane: %+v", s)
	}
	if s := got[idUntitle]; s.State != cs.StateIdle || !s.External || s.BlockId != "" {
		t.Fatalf("idle outside Bifrost: %+v", s)
	}
}

func TestStaleRunStatusWithoutProcessIsOffline(t *testing.T) {
	rows := fixtureRows()[:1]
	rows[0].status = "CASCADE_RUN_STATUS_RUNNING"
	dir := makeDir(t, rows, "")
	got := liveProvider(dir, map[string]liveProc{}, nil).Discover()
	if len(got) != 1 || got[0].State != cs.StateOffline || got[0].Pid != 0 {
		t.Fatalf("a dead process must read offline whatever agy last stored: %+v", got)
	}
}

func TestLiveConversationNotInDatabaseYet(t *testing.T) {
	dir := makeDir(t, fixtureRows()[:1], "")
	live := map[string]liveProc{idMulti: {Pid: 30, Cwd: "/home/me/new"}}
	var found *cs.ClaudeSession
	for _, s := range liveProvider(dir, live, map[int]string{30: "b"}).Discover() {
		if s.SessionId == idMulti {
			s := s
			found = &s
		}
	}
	if found == nil || found.State != cs.StateIdle || found.Cwd != "/home/me/new" || found.BlockId != "b" {
		t.Fatalf("got %+v", found)
	}
}

func TestLockConversationId(t *testing.T) {
	cases := map[string]string{
		"/home/me/.gemini/antigravity-cli/presence/" + idNamed + ".lock":    idNamed,
		"/home/me/.gemini/antigravity-cli/conversations/" + idNamed + ".db": "",
		"/home/me/other/" + idNamed + ".lock":                               "",
		"/x/presence/not-an-id.lock":                                        "",
	}
	for path, want := range cases {
		if got := lockConversationId(path); got != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}
}

func TestConversationArg(t *testing.T) {
	if got := conversationArg([]string{"agy", "--conversation", idNamed}); got != idNamed {
		t.Fatal(got)
	}
	if got := conversationArg([]string{"agy", "--conversation=" + idNamed}); got != idNamed {
		t.Fatal(got)
	}
	if got := conversationArg([]string{"agy", "-c"}); got != "" {
		t.Fatal(got)
	}
}

func launchProvider(t *testing.T, live map[string]liveProc) (*Provider, string) {
	t.Helper()
	real := t.TempDir()
	rows := fixtureRows()[:1]
	rows[0].uris = `["file://` + real + `"]`
	hist := `{"display":"one","timestamp":1,"conversationId":"` + idNamed + `"}
{"display":"/config","timestamp":5,"conversationId":"` + idNamed + `","type":"slash_command"}
{"display":"clear","timestamp":6,"conversationId":"` + idNamed + `"}
{"display":"two","timestamp":2,"conversationId":"` + idNamed + `"}
{"display":"other conversation","timestamp":3,"conversationId":"` + idUntitle + `"}
`
	p := liveProvider(makeDir(t, rows, hist), live, nil)
	p.lookPath = func(string) (string, error) { return "/usr/bin/agy", nil }
	return p, real
}

func TestPrepareResume(t *testing.T) {
	p, real := launchProvider(t, map[string]liveProc{})
	got, err := p.PrepareResume(idNamed, false)
	if err != nil || got.Cmd != "/usr/bin/agy" || got.Cwd != real || strings.Join(got.Args, " ") != "--conversation "+idNamed {
		t.Fatalf("resume: %+v %v", got, err)
	}
	skipped, err := p.PrepareResume(idNamed, true)
	if err != nil || strings.Join(skipped.Args, " ") != "--conversation "+idNamed+" "+cs.SkipPermissionsFlag {
		t.Fatalf("resume with skip permissions: %+v %v", skipped, err)
	}
	if _, err := p.PrepareResume("../etc/passwd", false); err == nil {
		t.Fatal("non-uuid id must be refused")
	}
	if _, err := p.PrepareResume(idMulti, false); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("unknown conversation: %v", err)
	}
	p.lookPath = func(string) (string, error) { return "", os.ErrNotExist }
	if _, err := p.PrepareResume(idNamed, false); err == nil || !strings.Contains(err.Error(), "PATH") {
		t.Fatalf("missing agy binary: %v", err)
	}
}

func TestPrepareResumeRefusesRunningAndMissingFolder(t *testing.T) {
	p, _ := launchProvider(t, map[string]liveProc{idNamed: {Pid: 1}})
	if _, err := p.PrepareResume(idNamed, false); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("running conversation must be refused: %v", err)
	}
	// idBadURI has no folder at all
	dir := makeDir(t, fixtureRows(), "")
	q := liveProvider(dir, map[string]liveProc{}, nil)
	q.lookPath = func(string) (string, error) { return "/usr/bin/agy", nil }
	if _, err := q.PrepareResume(idBadURI, false); err == nil || !strings.Contains(err.Error(), "folder") {
		t.Fatalf("missing folder must be refused: %v", err)
	}
}

func TestPrepareNew(t *testing.T) {
	p, real := launchProvider(t, nil)
	got, err := p.PrepareNew(real, false)
	if err != nil || got.Cwd != real || len(got.Args) != 0 {
		t.Fatalf("new: %+v %v", got, err)
	}
	if skipped, err := p.PrepareNew(real, true); err != nil || len(skipped.Args) != 1 || skipped.Args[0] != cs.SkipPermissionsFlag {
		t.Fatalf("new with skip permissions: %+v %v", skipped, err)
	}
	for _, bad := range []string{"", "relative", filepath.Join(real, "missing")} {
		if _, err := p.PrepareNew(bad, false); err == nil {
			t.Fatalf("%q must be refused", bad)
		}
	}
}

func TestRecentPrompts(t *testing.T) {
	p, _ := launchProvider(t, nil)
	got, err := p.RecentPrompts(idNamed, 10)
	if err != nil || len(got) != 2 || got[0].Text != "two" || got[1].Text != "one" {
		t.Fatalf("newest first, own conversation only, no noise: %+v %v", got, err)
	}
	if got, _ := p.RecentPrompts(idNamed, 1); len(got) != 1 {
		t.Fatalf("limit: %+v", got)
	}
	if _, err := p.RecentPrompts("bad", 5); err == nil {
		t.Fatal("non-uuid id must be refused")
	}
}
