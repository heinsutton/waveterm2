// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/wavetermdev/waveterm/pkg/waveobj"
	"github.com/wavetermdev/waveterm/pkg/wshrpc"
	"github.com/wavetermdev/waveterm/pkg/wshrpc/wshclient"
)

const (
	claudeStateBusy    = "busy"
	claudeStateIdle    = "idle"
	claudeStateWaiting = "waiting"
	claudeStateClear   = "clear"
	claudeHookMaxBytes = 1 << 20
)

var claudeStateCmd = &cobra.Command{
	Use:   "claudestate",
	Short: "record the state of the Claude Code session running in this block",
	Long: `Meant to be run as a Claude Code hook. Reads the hook's JSON from stdin and tags the current
block with the session id and its state (busy, idle or waiting for input), which the Claude Sessions
view shows. Add it to the UserPromptSubmit, PreToolUse (matcher AskUserQuestion), PostToolUse,
Notification (matchers permission_prompt and elicitation_dialog), Stop, SessionStart and SessionEnd
hooks.

Tools whose hook input has no event name (Antigravity's agy) pass the state explicitly with
--state busy|idle|waiting|clear; the session id is then read from "conversationId".`,
	Args:                  cobra.NoArgs,
	RunE:                  claudeStateRun,
	PreRunE:               preRunSetupRpcClient,
	DisableFlagsInUseLine: true,
}

var claudeStateFlag string

func init() {
	rootCmd.AddCommand(claudeStateCmd)
	claudeStateCmd.Flags().StringVar(&claudeStateFlag, "state", "", "set this state (busy, idle, waiting or clear) instead of deriving it from the hook event")
}

type claudeHookInput struct {
	HookEventName    string `json:"hook_event_name"`
	SessionId        string `json:"session_id"`
	ToolName         string `json:"tool_name"`
	NotificationType string `json:"notification_type"`
	ConversationId   string `json:"conversationId"` // Antigravity's name for the session id
}

// claudeStateFromFlag validates --state; "" means derive the state from the hook event.
func claudeStateFromFlag(flag string) (string, error) {
	switch flag {
	case "", claudeStateBusy, claudeStateIdle, claudeStateWaiting, claudeStateClear:
		return flag, nil
	}
	return "", fmt.Errorf("--state must be busy, idle, waiting or clear (got %q)", flag)
}

// sessionIdOf returns the session id from either tool's hook input.
func (in claudeHookInput) sessionIdOf() string {
	if in.SessionId != "" {
		return in.SessionId
	}
	return in.ConversationId
}

// claudeStateForHook maps a hook event to a session state; "" means the event does not change it.
func claudeStateForHook(in claudeHookInput) string {
	switch in.HookEventName {
	case "UserPromptSubmit", "PostToolUse":
		return claudeStateBusy
	case "PreToolUse":
		if in.ToolName == "AskUserQuestion" {
			return claudeStateWaiting
		}
		return claudeStateBusy
	case "Notification":
		if in.NotificationType == "" || in.NotificationType == "permission_prompt" || in.NotificationType == "elicitation_dialog" {
			return claudeStateWaiting
		}
		return ""
	case "Stop", "SessionStart":
		return claudeStateIdle
	case "SessionEnd":
		return claudeStateClear
	}
	return ""
}

func claudeStateRun(cmd *cobra.Command, args []string) (rtnErr error) {
	defer func() {
		sendActivity("claudestate", rtnErr == nil)
	}()
	data, err := io.ReadAll(io.LimitReader(os.Stdin, claudeHookMaxBytes))
	if err != nil {
		return fmt.Errorf("reading hook input: %v", err)
	}
	var in claudeHookInput
	if err := json.Unmarshal(data, &in); err != nil {
		return fmt.Errorf("parsing hook input: %v", err)
	}
	state, err := claudeStateFromFlag(claudeStateFlag)
	if err != nil {
		return err
	}
	if state == "" {
		state = claudeStateForHook(in)
	}
	sessionId := in.sessionIdOf()
	if state == "" || sessionId == "" {
		return nil
	}
	oref, err := resolveBlockArg()
	if err != nil {
		return fmt.Errorf("resolving block: %v", err)
	}
	if oref.OType != waveobj.OType_Block {
		return fmt.Errorf("claudestate needs a block (got %q)", oref.OType)
	}
	cur, err := wshclient.GetMetaCommand(RpcClient, wshrpc.CommandGetMetaData{ORef: *oref}, &wshrpc.RpcOpts{Timeout: 2000})
	if err != nil {
		return fmt.Errorf("reading block meta: %v", err)
	}
	var meta waveobj.MetaMapType
	if state == claudeStateClear {
		if !cur.HasKey(waveobj.MetaKey_ClaudeSession) {
			return nil
		}
		// A nil value deletes the key.
		meta = waveobj.MetaMapType{waveobj.MetaKey_ClaudeSession: nil, waveobj.MetaKey_ClaudeState: nil, waveobj.MetaKey_ClaudeStateTs: nil}
	} else {
		// Tool hooks fire constantly; skip the write when nothing changed.
		if cur.GetString(waveobj.MetaKey_ClaudeSession, "") == sessionId && cur.GetString(waveobj.MetaKey_ClaudeState, "") == state {
			return nil
		}
		meta = waveobj.MetaMapType{
			waveobj.MetaKey_ClaudeSession: sessionId,
			waveobj.MetaKey_ClaudeState:   state,
			waveobj.MetaKey_ClaudeStateTs: time.Now().UnixMilli(),
		}
	}
	err = wshclient.SetMetaCommand(RpcClient, wshrpc.CommandSetMetaData{ORef: *oref, Meta: meta}, &wshrpc.RpcOpts{Timeout: 2000})
	if err != nil {
		return fmt.Errorf("setting block meta: %v", err)
	}
	return nil
}
