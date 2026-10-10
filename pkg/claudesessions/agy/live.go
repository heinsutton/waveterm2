// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package agy

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/process"
	cs "github.com/wavetermdev/waveterm/pkg/claudesessions"
)

const (
	processName    = "agy"
	openFilesLimit = 2 * time.Second // a stuck fd must never stall the pane
	liveCacheTTL   = 2 * time.Second
	presenceDir    = "presence"
	lockSuffix     = ".lock"
)

// liveProc is a running agy process and the conversation it holds open.
type liveProc struct {
	Pid int
	Cwd string
}

// findLive maps conversation id -> running process. agy keeps presence/<id>.lock open for as long
// as it runs a conversation, so the open files give the exact id whatever the command line says;
// a `--conversation <id>` argument is the fallback when the files cannot be read.
func findLive() map[string]liveProc {
	live := map[string]liveProc{}
	procs, err := process.Processes()
	if err != nil {
		return live
	}
	for _, proc := range procs {
		if name, err := proc.Name(); err != nil || name != processName {
			continue
		}
		id := conversationOf(proc)
		if !cs.IsSessionId(id) {
			continue
		}
		cwd, _ := proc.Cwd()
		live[id] = liveProc{Pid: int(proc.Pid), Cwd: cwd}
	}
	return live
}

func conversationOf(proc *process.Process) string {
	type result struct{ id string }
	ch := make(chan result, 1)
	go func() {
		files, err := proc.OpenFiles()
		if err != nil {
			ch <- result{}
			return
		}
		for _, f := range files {
			if id := lockConversationId(f.Path); id != "" {
				ch <- result{id}
				return
			}
		}
		ch <- result{}
	}()
	select {
	case r := <-ch:
		if r.id != "" {
			return r.id
		}
	case <-time.After(openFilesLimit):
	}
	args, err := proc.CmdlineSlice()
	if err != nil {
		return ""
	}
	return conversationArg(args)
}

// lockConversationId returns the id of a ".../presence/<id>.lock" path, else "".
func lockConversationId(path string) string {
	if filepath.Base(filepath.Dir(path)) != presenceDir || !strings.HasSuffix(path, lockSuffix) {
		return ""
	}
	id := strings.TrimSuffix(filepath.Base(path), lockSuffix)
	if !cs.IsSessionId(id) {
		return ""
	}
	return id
}

// conversationArg finds the id after `--conversation` (or `--conversation=<id>`).
func conversationArg(args []string) string {
	for i, a := range args {
		if v, ok := strings.CutPrefix(a, "--conversation="); ok {
			return v
		}
		if a == "--conversation" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// liveNow returns findLive, reused for a moment so rapid polls do not rescan every process.
func (p *Provider) liveNow() map[string]liveProc {
	p.lock.Lock()
	if p.liveAt.Add(liveCacheTTL).After(time.Now()) && p.liveCache != nil {
		cached := p.liveCache
		p.lock.Unlock()
		return cached
	}
	p.lock.Unlock()
	live := p.findLive()
	p.lock.Lock()
	p.liveCache, p.liveAt = live, time.Now()
	p.lock.Unlock()
	return live
}

const (
	// Seen by watching a conversation while a permission prompt was open: the tool-call step
	// (type 132) sits at status 9 for as long as agy waits for the user, then turns 3 (done).
	stepTypeToolCall     = 132
	stepStatusAwaiting   = 9
	conversationsDirName = "conversations"
)

// stepAwaitsApproval reports whether the newest step of a conversation is a tool call waiting for
// the user (a permission prompt or a question). Any problem reading the file reads as "no".
func (p *Provider) stepAwaitsApproval(id string) bool {
	path := filepath.Join(p.dir, conversationsDirName, id+".db")
	if _, err := os.Stat(path); err != nil {
		return false
	}
	db, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?mode=ro&_busy_timeout=%d", path, dbTimeout))
	if err != nil {
		return false
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	var stepType, status int
	if db.QueryRow("SELECT step_type, status FROM steps ORDER BY idx DESC LIMIT 1").Scan(&stepType, &status) != nil {
		return false
	}
	return stepType == stepTypeToolCall && status == stepStatusAwaiting
}
