// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package claudesessions

import (
	"fmt"
	"sort"
)

// Harness is one coding agent whose sessions the pane lists (Claude Code, Antigravity, ...).
// Implementations live in their own package or file and must not import wshrpc.
type Harness interface {
	Name() string
	Discover() []ClaudeSession
	PrepareResume(sessionId string) (*ClaudeLaunch, error)
	PrepareNew(cwd string) (*ClaudeLaunch, error)
	RecentPrompts(sessionId string, limit int) ([]ClaudePrompt, error)
}

var _ Harness = (*Provider)(nil)

// Name is the harness key stored in ClaudeSession.Harness.
func (p *Provider) Name() string { return HarnessClaude }

// FindHarness returns the harness with the given name; an empty name means Claude Code.
func FindHarness(harnesses []Harness, name string) (Harness, error) {
	if name == "" {
		name = HarnessClaude
	}
	for _, h := range harnesses {
		if h.Name() == name {
			return h, nil
		}
	}
	return nil, fmt.Errorf("unknown harness: %q", name)
}

// discoverAll merges the sessions of every harness, newest first.
func discoverAll(harnesses []Harness) []ClaudeSession {
	sessions := []ClaudeSession{}
	for _, h := range harnesses {
		sessions = append(sessions, h.Discover()...)
	}
	sort.SliceStable(sessions, func(i, j int) bool {
		if sessions[i].LastActive != sessions[j].LastActive {
			return sessions[i].LastActive > sessions[j].LastActive
		}
		return sessions[i].SessionId < sessions[j].SessionId
	})
	return sessions
}
