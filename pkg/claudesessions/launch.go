// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package claudesessions

import "fmt"

const claudeBinary = "claude"

func (p *Provider) claudePath() (string, error) {
	path, err := p.lookPath(claudeBinary)
	if err != nil {
		return "", fmt.Errorf("%s was not found on the PATH Bifrost runs with", claudeBinary)
	}
	return path, nil
}

// PrepareResume checks, against fresh data, that a session can be resumed and returns the command.
// A session that is alive anywhere (Bifrost or another terminal) is refused: two copies of one
// session must never run.
func (p *Provider) PrepareResume(sessionId string, skipPermissions bool) (*ClaudeLaunch, error) {
	if !IsSessionId(sessionId) {
		return nil, fmt.Errorf("not a session id: %q", sessionId)
	}
	if _, live := p.readRegistry()[sessionId]; live {
		return nil, fmt.Errorf("session is already running")
	}
	var found *ClaudeSession
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
	cwd, err := CheckFolder(found.Cwd)
	if err != nil {
		return nil, err
	}
	bin, err := p.claudePath()
	if err != nil {
		return nil, err
	}
	return &ClaudeLaunch{Cmd: bin, Args: LaunchArgs([]string{"-r", sessionId}, skipPermissions), Cwd: cwd}, nil
}

// PrepareNew returns the command that starts a fresh session in a folder.
func (p *Provider) PrepareNew(cwd string, skipPermissions bool) (*ClaudeLaunch, error) {
	clean, err := CheckFolder(cwd)
	if err != nil {
		return nil, err
	}
	bin, err := p.claudePath()
	if err != nil {
		return nil, err
	}
	return &ClaudeLaunch{Cmd: bin, Args: LaunchArgs([]string{}, skipPermissions), Cwd: clean}, nil
}
