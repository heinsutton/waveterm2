// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package cmd

import "testing"

func TestClaudeStateForHook(t *testing.T) {
	cases := []struct {
		in   claudeHookInput
		want string
	}{
		{claudeHookInput{HookEventName: "UserPromptSubmit"}, claudeStateBusy},
		{claudeHookInput{HookEventName: "PostToolUse", ToolName: "Bash"}, claudeStateBusy},
		{claudeHookInput{HookEventName: "PreToolUse", ToolName: "Bash"}, claudeStateBusy},
		{claudeHookInput{HookEventName: "PreToolUse", ToolName: "AskUserQuestion"}, claudeStateWaiting},
		{claudeHookInput{HookEventName: "Notification", NotificationType: "permission_prompt"}, claudeStateWaiting},
		{claudeHookInput{HookEventName: "Notification", NotificationType: "elicitation_dialog"}, claudeStateWaiting},
		{claudeHookInput{HookEventName: "Notification", NotificationType: "idle_prompt"}, ""},
		{claudeHookInput{HookEventName: "Stop"}, claudeStateIdle},
		{claudeHookInput{HookEventName: "SessionStart"}, claudeStateIdle},
		{claudeHookInput{HookEventName: "SessionEnd"}, claudeStateClear},
		{claudeHookInput{HookEventName: "PreCompact"}, ""},
	}
	for _, c := range cases {
		if got := claudeStateForHook(c.in); got != c.want {
			t.Errorf("%+v: got %q, want %q", c.in, got, c.want)
		}
	}
}

func TestClaudeStateFlagAndSessionId(t *testing.T) {
	for _, ok := range []string{"", "busy", "idle", "waiting", "clear"} {
		if _, err := claudeStateFromFlag(ok); err != nil {
			t.Errorf("%q must be accepted: %v", ok, err)
		}
	}
	if _, err := claudeStateFromFlag("running"); err == nil {
		t.Error("an unknown state must be refused")
	}
	if got := (claudeHookInput{SessionId: "a", ConversationId: "b"}).sessionIdOf(); got != "a" {
		t.Errorf("session_id wins, got %q", got)
	}
	if got := (claudeHookInput{ConversationId: "b"}).sessionIdOf(); got != "b" {
		t.Errorf("agy conversationId, got %q", got)
	}
}
