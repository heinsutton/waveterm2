// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

// Package agy lists Google Antigravity CLI (`agy`) conversations for the sessions pane. It uses
// SQLite (cgo), so it is linked into wavesrv only; wsh and wshrpc must never import it.
package agy

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
	cs "github.com/wavetermdev/waveterm/pkg/claudesessions"
)

const (
	HarnessName     = "agy"
	summariesDBName = "conversation_summaries.db"
	dbTimeout       = 2000 // ms to wait for agy's write lock
	previewMaxLen   = 300
	maxPromptsLimit = 50
	zeroTimePrefix  = "0001-01-01"

	runStatusRunning   = "CASCADE_RUN_STATUS_RUNNING"
	runStatusBusy      = "CASCADE_RUN_STATUS_BUSY"
	runStatusCanceling = "CASCADE_RUN_STATUS_CANCELING"
)

// bookkeeping lines that say nothing about what a conversation was doing
var noisePrompts = map[string]bool{"/exit": true, "/clear": true, "clear": true, "exit": true}

// the columns read from conversation_summaries; a column agy dropped reads as NULL
var summaryColumns = []string{
	"conversation_id", "title", "preview", "step_count", "last_modified_time", "last_user_input_time",
	"workspace_uris", "status", "parent_conversation_id", "nesting_depth", "killed",
}

// Provider reads agy's data directory (normally ~/.gemini/antigravity-cli).
type Provider struct {
	dir string

	lock       sync.Mutex
	rowsKey    string
	rows       []summaryRow
	historyKey string
	history    map[string]historyLine

	findLive  func() map[string]liveProc
	blockOf   func(pid int) string
	lookPath  func(file string) (string, error)
	awaiting  func(id string) bool // the conversation's newest step waits for the user's approval
	liveAt    time.Time
	liveCache map[string]liveProc
}

func MakeProvider(agyDir string) *Provider {
	p := &Provider{dir: agyDir, findLive: findLive, blockOf: cs.BlockOfPid, lookPath: exec.LookPath}
	p.awaiting = p.stepAwaitsApproval
	return p
}

var _ cs.Harness = (*Provider)(nil)

func (p *Provider) Name() string { return HarnessName }

type summaryRow struct {
	id        string
	title     string
	preview   string
	steps     int64
	modified  int64 // unix ms, 0 when agy stored the zero time
	userInput int64
	cwd       string
	status    string
}

type historyLine struct {
	ts      int64
	display string
}

type historyEntry struct {
	Display        string `json:"display"`
	Timestamp      int64  `json:"timestamp"`
	ConversationId string `json:"conversationId"`
	Type           string `json:"type"`
}

// parseTime reads agy's datetime text ("2006-01-02 15:04:05.999999999-07:00"); the zero time and
// anything unparseable give 0.
func parseTime(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" || strings.HasPrefix(s, zeroTimePrefix) {
		return 0
	}
	for _, layout := range []string{"2006-01-02 15:04:05.999999999-07:00", time.RFC3339Nano} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UnixMilli()
		}
	}
	return 0
}

// workspaceCwd turns the first entry of workspace_uris (a JSON list of file:// URIs) into a path.
func workspaceCwd(raw string) string {
	var uris []string
	if json.Unmarshal([]byte(raw), &uris) != nil {
		return ""
	}
	for _, u := range uris {
		parsed, err := url.Parse(u)
		if err != nil || parsed.Scheme != "file" || parsed.Path == "" {
			continue
		}
		return filepath.Clean(filepath.FromSlash(parsed.Path))
	}
	return ""
}

func fileKey(path string) string {
	fi, err := os.Stat(path)
	if err != nil {
		return "-"
	}
	return fmt.Sprintf("%d:%d", fi.ModTime().UnixNano(), fi.Size())
}

// loadRows queries the summaries database read-only. A missing, locked or damaged database gives
// no rows; the pane must keep working for the other harnesses.
func (p *Provider) loadRows() []summaryRow {
	dbPath := filepath.Join(p.dir, summariesDBName)
	key := fileKey(dbPath) + "|" + fileKey(dbPath+"-wal")
	p.lock.Lock()
	defer p.lock.Unlock()
	if key == p.rowsKey && p.rows != nil {
		return p.rows
	}
	if _, err := os.Stat(dbPath); err != nil {
		return nil
	}
	rows := queryRows(dbPath)
	if rows == nil {
		return nil
	}
	p.rowsKey, p.rows = key, rows
	return rows
}

func queryRows(dbPath string) []summaryRow {
	db, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?mode=ro&_busy_timeout=%d", dbPath, dbTimeout))
	if err != nil {
		return nil
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	have := map[string]bool{}
	info, err := db.Query("SELECT name FROM pragma_table_info('conversation_summaries')")
	if err != nil {
		return nil
	}
	for info.Next() {
		var name string
		if info.Scan(&name) == nil {
			have[name] = true
		}
	}
	info.Close()
	if !have["conversation_id"] {
		return nil
	}
	exprs := make([]string, len(summaryColumns))
	for i, c := range summaryColumns {
		if have[c] {
			exprs[i] = "CAST(" + c + " AS TEXT)"
		} else {
			exprs[i] = "NULL"
		}
	}
	res, err := db.Query("SELECT " + strings.Join(exprs, ", ") + " FROM conversation_summaries")
	if err != nil {
		return nil
	}
	defer res.Close()
	out := []summaryRow{}
	for res.Next() {
		var id, title, preview, steps, modified, userInput, uris, status, parent, depth, killed sql.NullString
		if res.Scan(&id, &title, &preview, &steps, &modified, &userInput, &uris, &status, &parent, &depth, &killed) != nil {
			continue
		}
		if !cs.IsSessionId(id.String) || parent.String != "" || isTrue(killed.String) || (depth.String != "" && depth.String != "0") {
			continue // sub-agent conversations and killed ones are not sessions the user opened
		}
		var stepCount int64
		fmt.Sscan(steps.String, &stepCount)
		out = append(out, summaryRow{
			id:        id.String,
			title:     strings.TrimSpace(title.String),
			preview:   strings.TrimSpace(preview.String),
			steps:     stepCount,
			modified:  parseTime(modified.String),
			userInput: parseTime(userInput.String),
			cwd:       workspaceCwd(uris.String),
			status:    status.String,
		})
	}
	if res.Err() != nil {
		return nil
	}
	return out
}

func isTrue(s string) bool { return s == "1" || strings.EqualFold(s, "true") }

// readHistory returns the newest prompt per conversation from agy's history.jsonl.
func (p *Provider) readHistory() map[string]historyLine {
	path := filepath.Join(p.dir, "history.jsonl")
	key := fileKey(path)
	p.lock.Lock()
	defer p.lock.Unlock()
	if key == p.historyKey && p.history != nil {
		return p.history
	}
	out := map[string]historyLine{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		var he historyEntry
		if json.Unmarshal(sc.Bytes(), &he) != nil || he.ConversationId == "" || he.Type == "slash_command" || noisePrompts[strings.TrimSpace(he.Display)] {
			continue
		}
		if cur, ok := out[he.ConversationId]; !ok || he.Timestamp >= cur.ts {
			out[he.ConversationId] = historyLine{ts: he.Timestamp, display: he.Display}
		}
	}
	p.historyKey, p.history = key, out
	return out
}

func clip(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > previewMaxLen {
		return string(r[:previewMaxLen-1]) + "…"
	}
	return s
}

// Discover lists every conversation that has been used, newest first. Conversations a running
// agy holds open are marked busy or idle; the rest are offline.
func (p *Provider) Discover() []cs.ClaudeSession {
	rows := p.loadRows()
	history := p.readHistory()
	sessions := make([]cs.ClaudeSession, 0, len(rows))
	for _, r := range rows {
		h := history[r.id]
		last := r.userInput
		if last == 0 {
			last = max(r.modified, h.ts)
		}
		if last == 0 && r.steps == 0 && r.title == "" {
			continue // opened and closed without a single message
		}
		preview := r.preview
		if h.display != "" {
			preview = h.display
		}
		sessions = append(sessions, cs.ClaudeSession{
			Harness:    HarnessName,
			SessionId:  r.id,
			Name:       r.title,
			Cwd:        r.cwd,
			LastActive: last,
			Preview:    clip(preview),
			State:      cs.StateOffline,
		})
	}
	sessions = p.applyLive(sessions, rows)
	sort.Slice(sessions, func(i, j int) bool {
		if sessions[i].LastActive != sessions[j].LastActive {
			return sessions[i].LastActive > sessions[j].LastActive
		}
		return sessions[i].SessionId < sessions[j].SessionId
	})
	return sessions
}

const agyBinary = "agy"

func (p *Provider) agyPath() (string, error) {
	path, err := p.lookPath(agyBinary)
	if err != nil {
		return "", fmt.Errorf("%s was not found on the PATH Bifrost runs with", agyBinary)
	}
	return path, nil
}

// PrepareResume checks, against fresh data, that a conversation can be resumed and returns the
// command. A conversation held open by any agy process is refused: two copies must never run.
// Available reports whether the agy binary is on the PATH.
func (p *Provider) Available() bool {
	_, err := p.lookPath(agyBinary)
	return err == nil
}

func (p *Provider) PrepareResume(sessionId string, skipPermissions bool) (*cs.ClaudeLaunch, error) {
	if !cs.IsSessionId(sessionId) {
		return nil, fmt.Errorf("not a session id: %q", sessionId)
	}
	if _, live := p.findLive()[sessionId]; live {
		return nil, fmt.Errorf("session is already running")
	}
	var found *cs.ClaudeSession
	for _, s := range p.Discover() {
		if s.SessionId == sessionId {
			s := s
			found = &s
			break
		}
	}
	if found == nil {
		return nil, fmt.Errorf("session not found")
	}
	cwd, err := cs.CheckFolder(found.Cwd)
	if err != nil {
		return nil, err
	}
	bin, err := p.agyPath()
	if err != nil {
		return nil, err
	}
	return &cs.ClaudeLaunch{Cmd: bin, Args: cs.LaunchArgs([]string{"--conversation", sessionId}, skipPermissions), Cwd: cwd}, nil
}

// PrepareNew returns the command that starts a fresh conversation in a folder. agy cannot be named
// at startup, so name is ignored.
func (p *Provider) PrepareNew(cwd string, skipPermissions bool, name string) (*cs.ClaudeLaunch, error) {
	clean, err := cs.CheckFolder(cwd)
	if err != nil {
		return nil, err
	}
	bin, err := p.agyPath()
	if err != nil {
		return nil, err
	}
	return &cs.ClaudeLaunch{Cmd: bin, Args: cs.LaunchArgs([]string{}, skipPermissions), Cwd: clean}, nil
}

// RecentPrompts returns the newest prompts typed in a conversation, from agy's prompt history.
func (p *Provider) RecentPrompts(sessionId string, limit int) ([]cs.ClaudePrompt, error) {
	if !cs.IsSessionId(sessionId) {
		return nil, fmt.Errorf("not a session id: %q", sessionId)
	}
	limit = min(max(limit, 1), maxPromptsLimit)
	f, err := os.Open(filepath.Join(p.dir, "history.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return []cs.ClaudePrompt{}, nil
		}
		return nil, err
	}
	defer f.Close()
	prompts := []cs.ClaudePrompt{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		var he historyEntry
		if json.Unmarshal(sc.Bytes(), &he) != nil || he.ConversationId != sessionId || he.Type == "slash_command" {
			continue
		}
		text := clip(he.Display)
		if text == "" || noisePrompts[text] {
			continue
		}
		prompts = append(prompts, cs.ClaudePrompt{Ts: he.Timestamp, Text: text})
	}
	sort.SliceStable(prompts, func(i, j int) bool { return prompts[i].Ts > prompts[j].Ts })
	if len(prompts) > limit {
		prompts = prompts[:limit]
	}
	return prompts, nil
}

// applyLive marks the conversations a running agy has open. Busy comes from the summaries run
// status; a conversation that is not in the database yet (just started) is added from the process.
func (p *Provider) applyLive(sessions []cs.ClaudeSession, rows []summaryRow) []cs.ClaudeSession {
	live := p.liveNow()
	if len(live) == 0 {
		return sessions
	}
	status := make(map[string]string, len(rows))
	modified := make(map[string]int64, len(rows))
	for _, r := range rows {
		status[r.id] = r.status
		modified[r.id] = r.modified
	}
	index := make(map[string]int, len(sessions))
	for i, s := range sessions {
		index[s.SessionId] = i
	}
	for id, lp := range live {
		i, ok := index[id]
		if !ok {
			sessions = append(sessions, cs.ClaudeSession{Harness: HarnessName, SessionId: id, Cwd: lp.Cwd, LastActive: time.Now().UnixMilli()})
			i = len(sessions) - 1
		}
		s := &sessions[i]
		s.Pid = lp.Pid
		s.BlockId = p.blockOf(lp.Pid)
		s.External = s.BlockId == ""
		s.State = cs.StateIdle
		if st := status[id]; st == runStatusRunning || st == runStatusBusy || st == runStatusCanceling {
			s.State = cs.StateBusy
		}
		s.Status = s.State
		if s.State == cs.StateBusy && p.awaiting(id) {
			s.State = cs.StateWaiting
		}
		s.StatusTs = modified[id] // lets a newer idle in the database beat a stale busy hook
		if s.Cwd == "" {
			s.Cwd = lp.Cwd
		}
	}
	return sessions
}
