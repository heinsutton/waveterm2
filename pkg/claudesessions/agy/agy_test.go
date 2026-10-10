// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package agy

import (
	"database/sql"
	"os"
	"path/filepath"
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
	id, title, preview, modified, input, uris, parent string
	steps, depth                                      int
	killed                                            bool
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
		if _, err := db.Exec(`INSERT INTO conversation_summaries (conversation_id,title,preview,step_count,last_modified_time,workspace_uris,parent_conversation_id,nesting_depth,killed,last_user_input_time) VALUES (?,?,?,?,?,?,?,?,?,?)`,
			r.id, r.title, r.preview, r.steps, r.modified, r.uris, r.parent, r.depth, r.killed, r.input); err != nil {
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
