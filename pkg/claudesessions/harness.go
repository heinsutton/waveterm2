// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package claudesessions

import (
	"fmt"
	"sort"
	"strings"
)

// Harness is one coding agent whose sessions the pane lists (Claude Code, Antigravity, ...).
// Implementations live in their own package or file and must not import wshrpc.
type Harness interface {
	Name() string
	Available() bool // the tool's binary is on the PATH
	Discover() []ClaudeSession
	PrepareResume(sessionId string, skipPermissions bool) (*ClaudeLaunch, error)
	PrepareNew(cwd string, skipPermissions bool, name string) (*ClaudeLaunch, error) // name "" = none; a tool that cannot be named at startup ignores it
	RecentPrompts(sessionId string, limit int) ([]ClaudePrompt, error)
}

var _ Harness = (*Provider)(nil)

// Name is the harness key stored in ClaudeSession.Harness.
func (p *Provider) Name() string { return HarnessClaude }

// Available reports whether the claude binary is on the PATH.
func (p *Provider) Available() bool {
	_, err := p.lookPath(claudeBinary)
	return err == nil
}

// SkipPermissionsFlag makes both claude and agy approve every tool call without asking.
const SkipPermissionsFlag = "--dangerously-skip-permissions"

// LaunchArgs appends the skip-permissions flag to args when the user chose it.
func LaunchArgs(args []string, skipPermissions bool) []string {
	if skipPermissions {
		return append(args, SkipPermissionsFlag)
	}
	return args
}

const maxLaunchNameLen = 200

// CleanLaunchName trims a user-typed session name and rejects control characters.
func CleanLaunchName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if len([]rune(name)) > maxLaunchNameLen {
		return "", fmt.Errorf("name is longer than %d characters", maxLaunchNameLen)
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("name contains a control character")
		}
	}
	return name, nil
}

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
