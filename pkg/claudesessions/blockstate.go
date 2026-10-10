// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package claudesessions

// BlockClaude is what the `wsh claudestate` hook left on one Bifrost pane.
type BlockClaude struct {
	BlockId   string
	SessionId string
	State     string
	Ts        int64 // unix ms the hook ran
}

func validHookState(state string) bool {
	return state == StateBusy || state == StateIdle || state == StateWaiting
}

// ApplyBlockStates refines the registry's busy/idle with what the hooks reported for the pane
// running each session. A hook state only counts while the process is alive (a dead session stays
// offline, so stale pane metadata cannot make it look running), and the registry wins when it
// reports idle after the hook ran (the turn ended without a Stop hook, e.g. an interrupt).
func ApplyBlockStates(sessions []ClaudeSession, blocks []BlockClaude) {
	newest := make(map[string]BlockClaude)
	for _, b := range blocks {
		if cur, ok := newest[b.SessionId]; !ok || b.Ts >= cur.Ts {
			newest[b.SessionId] = b
		}
	}
	for i := range sessions {
		s := &sessions[i]
		b, ok := newest[s.SessionId]
		if !ok || s.State == StateOffline {
			continue
		}
		if s.External {
			continue
		}
		s.BlockId = b.BlockId
		if !validHookState(b.State) {
			continue
		}
		if s.Status == StateIdle && s.StatusTs > b.Ts && b.State != StateIdle {
			continue
		}
		if s.State == StateWaiting && b.State == StateBusy {
			continue // the provider saw the session waiting right now; a busy hook is older news
		}
		s.State = b.State
	}
}
