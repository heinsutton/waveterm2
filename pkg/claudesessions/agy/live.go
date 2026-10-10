// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package agy

import (
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
