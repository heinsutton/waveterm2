// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package claudesessions

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/shirou/gopsutil/v4/process"
)

var sessionIdRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var registryFileRe = regexp.MustCompile(`^\d+\.json$`)

func IsSessionId(s string) bool {
	return sessionIdRe.MatchString(s)
}

type registryEntry struct {
	Pid             int    `json:"pid"`
	SessionId       string `json:"sessionId"`
	Cwd             string `json:"cwd"`
	Kind            string `json:"kind"`
	Name            string `json:"name"`
	Status          string `json:"status"`
	Version         string `json:"version"`
	UpdatedAt       int64  `json:"updatedAt"`
	StatusUpdatedAt int64  `json:"statusUpdatedAt"`
}

type historyEntry struct {
	Display   string `json:"display"`
	Timestamp int64  `json:"timestamp"`
	Project   string `json:"project"`
	SessionId string `json:"sessionId"`
}

type cacheKey struct {
	mtime int64
	size  int64
}

type cacheEntry struct {
	key  cacheKey
	info transcriptInfo
}

// Provider reads Claude Code's files under claudeDir (normally ~/.claude).
type Provider struct {
	claudeDir string
	pidAlive  func(pid int) bool
	blockOf   func(pid int) string
	lookPath  func(file string) (string, error)
	lock      sync.Mutex
	cache     map[string]cacheEntry
}

func MakeProvider(claudeDir string) *Provider {
	return &Provider{claudeDir: claudeDir, pidAlive: pidAlive, blockOf: blockOfPid, lookPath: exec.LookPath, cache: make(map[string]cacheEntry)}
}

func pidAlive(pid int) bool {
	ok, err := process.PidExists(int32(pid))
	return err == nil && ok
}

// BlockOfPid is blockOfPid for other harnesses.
func BlockOfPid(pid int) string { return blockOfPid(pid) }

// blockOfPid returns the Bifrost block the process was started in: panes put WAVETERM_BLOCKID in
// the environment, and Claude inherits it from the shell. Empty when it cannot be read or the
// process was started elsewhere (another terminal).
func blockOfPid(pid int) string {
	proc, err := process.NewProcess(int32(pid))
	if err != nil {
		return ""
	}
	env, err := proc.Environ()
	if err != nil {
		return ""
	}
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "WAVETERM_BLOCKID="); ok {
			return v
		}
	}
	return ""
}

func (p *Provider) readRegistry() map[string]registryEntry {
	out := make(map[string]registryEntry)
	dir := filepath.Join(p.claudeDir, "sessions")
	ents, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range ents {
		if e.IsDir() || !registryFileRe.MatchString(e.Name()) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var re registryEntry
		if json.Unmarshal(data, &re) != nil || re.Pid <= 0 || !IsSessionId(re.SessionId) || re.Kind == "sdk" {
			continue
		}
		if !p.pidAlive(re.Pid) {
			continue
		}
		out[re.SessionId] = re
	}
	return out
}

// readHistory returns the newest prompt entry per session id; a missing or damaged file gives an
// empty result.
func (p *Provider) readHistory() map[string]historyEntry {
	out := make(map[string]historyEntry)
	f, err := os.Open(filepath.Join(p.claudeDir, "history.jsonl"))
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		var he historyEntry
		if json.Unmarshal(sc.Bytes(), &he) != nil || !IsSessionId(he.SessionId) {
			continue
		}
		if old, ok := out[he.SessionId]; !ok || he.Timestamp >= old.Timestamp {
			out[he.SessionId] = he
		}
	}
	return out
}

func (p *Provider) transcript(path string, mtime int64, size int64) transcriptInfo {
	key := cacheKey{mtime: mtime, size: size}
	p.lock.Lock()
	ce, ok := p.cache[path]
	p.lock.Unlock()
	if ok && ce.key == key {
		return ce.info
	}
	info, _ := readTranscript(path, size)
	p.lock.Lock()
	p.cache[path] = cacheEntry{key: key, info: info}
	p.lock.Unlock()
	return info
}

// decodeProjectDir is a last resort for a session with no cwd anywhere: "-home-a-b" becomes
// "/home/a/b". It is lossy (a real "-" is ambiguous), which is why it is only used as a fallback.
func decodeProjectDir(name string) string {
	if !strings.HasPrefix(name, "-") {
		return ""
	}
	return strings.ReplaceAll(name, "-", "/")
}

// Discover lists every session: closed ones from the transcripts, live ones from the registry.
func (p *Provider) Discover() []ClaudeSession {
	registry := p.readRegistry()
	history := p.readHistory()
	seen := make(map[string]bool)
	var sessions []ClaudeSession

	projectsDir := filepath.Join(p.claudeDir, "projects")
	dirs, _ := os.ReadDir(projectsDir)
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(projectsDir, d.Name()))
		if err != nil {
			continue
		}
		for _, fe := range files {
			id := strings.TrimSuffix(fe.Name(), ".jsonl")
			if fe.IsDir() || id == fe.Name() || !IsSessionId(id) || seen[id] {
				continue
			}
			fi, err := fe.Info()
			if err != nil {
				continue
			}
			path := filepath.Join(projectsDir, d.Name(), fe.Name())
			info := p.transcript(path, fi.ModTime().UnixMilli(), fi.Size())
			// File mtime also moves when a session is renamed, so the last real message wins.
			lastActive := info.LastTs
			if lastActive == 0 {
				lastActive = fi.ModTime().UnixMilli()
			}
			s := ClaudeSession{Harness: HarnessClaude, SessionId: id, Name: info.Name, Cwd: info.Cwd, Preview: info.Preview, LastActive: lastActive}
			if he, ok := history[id]; ok {
				if s.Cwd == "" {
					s.Cwd = he.Project
				}
				if s.Preview == "" {
					s.Preview = cleanText(he.Display, maxPreviewLen)
				}
			}
			if s.Cwd == "" {
				s.Cwd = decodeProjectDir(d.Name())
			}
			seen[id] = true
			sessions = append(sessions, s)
		}
	}
	// Live sessions whose transcript does not exist yet (just started).
	for id, re := range registry {
		if !seen[id] {
			sessions = append(sessions, ClaudeSession{Harness: HarnessClaude, SessionId: id, Cwd: re.Cwd, LastActive: re.UpdatedAt})
			seen[id] = true
		}
	}
	for i := range sessions {
		sessions[i].State = StateOffline
		if re, ok := registry[sessions[i].SessionId]; ok {
			s := &sessions[i]
			s.Pid = re.Pid
			s.Status = re.Status
			s.StatusTs = re.StatusUpdatedAt
			s.BlockId = p.blockOf(re.Pid)
			s.External = s.BlockId == ""
			s.Version = re.Version
			s.State = StateIdle
			if re.Status == StateBusy {
				s.State = StateBusy
			}
			if re.Name != "" {
				s.Name = cleanText(re.Name, maxNameLen)
			}
			if s.Cwd == "" {
				s.Cwd = re.Cwd
			}
			if re.UpdatedAt > s.LastActive {
				s.LastActive = re.UpdatedAt
			}
		}
	}
	sort.Slice(sessions, func(i, j int) bool {
		if sessions[i].LastActive != sessions[j].LastActive {
			return sessions[i].LastActive > sessions[j].LastActive
		}
		return sessions[i].SessionId < sessions[j].SessionId
	})
	return sessions
}
