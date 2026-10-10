// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package claudesessions

const HarnessClaude = "claude"

const (
	StateBusy    = "busy"
	StateIdle    = "idle"
	StateWaiting = "waiting"
	StateOffline = "offline"
)

// ClaudeSession is one Claude Code session found under ~/.claude, open or closed.
type ClaudeSession struct {
	Harness    string `json:"harness"`
	SessionId  string `json:"sessionid"`
	Name       string `json:"name,omitempty"`
	Cwd        string `json:"cwd"`
	LastActive int64  `json:"lastactive"` // unix ms
	Preview    string `json:"preview,omitempty"`
	Pid        int    `json:"pid,omitempty"`    // only set while the process is alive
	Status     string `json:"status,omitempty"` // registry status of a live session: busy | idle
	StatusTs   int64  `json:"statusts,omitempty"`
	State      string `json:"state"`              // busy | idle | waiting | offline
	Hidden     bool   `json:"hidden,omitempty"`   // the user removed it from the list (its files are untouched)
	External   bool   `json:"external,omitempty"` // alive but not started in a Bifrost pane (e.g. another terminal)
	BlockId    string `json:"blockid,omitempty"`  // the Bifrost pane running the session, when a hook tagged one
	Version    string `json:"version,omitempty"`
}

// ClaudeFolder is a directory the user asked to remember.
type ClaudeFolder struct {
	Path  string `json:"path"`
	Label string `json:"label,omitempty"`
}

// ClaudeHarnessInfo says which tools the pane can start; a tool whose binary is missing is listed
// as unavailable so the pane can show it disabled.
type ClaudeHarnessInfo struct {
	Name      string `json:"name"`
	Available bool   `json:"available"`
}

// ClaudeListResult is what the pane shows: every session plus the user's own folders and descriptions.
type ClaudeListResult struct {
	Sessions     []ClaudeSession     `json:"sessions"`
	Folders      []ClaudeFolder      `json:"folders"`
	Descriptions map[string]string   `json:"descriptions"`
	Harnesses    []ClaudeHarnessInfo `json:"harnesses"`
	Missing      []string            `json:"missing"` // folders (of sessions or remembered) that no longer exist
	Ts           int64               `json:"ts"`
}

// ClaudeLaunch is the command a new pane runs to resume a session or start a fresh one.
type ClaudeLaunch struct {
	Cmd  string   `json:"cmd"`
	Args []string `json:"args"`
	Cwd  string   `json:"cwd"`
}

// ClaudePrompt is one prompt the user typed in a session, newest first in lists.
type ClaudePrompt struct {
	Ts   int64  `json:"ts"` // unix ms
	Text string `json:"text"`
}
