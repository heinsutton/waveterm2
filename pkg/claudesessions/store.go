// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package claudesessions

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const StoreFileName = "claude-sessions.json"

// storeData is the user's own data: folders to remember and a description per session. It lives in
// Bifrost's config dir, never in ~/.claude (owned by Claude Code) or the vault.
type storeData struct {
	Folders      []ClaudeFolder    `json:"folders"`
	Descriptions map[string]string `json:"descriptions"`
	Hidden       []string          `json:"hidden"`
}

func loadStore(configDir string) storeData {
	sd := storeData{Folders: []ClaudeFolder{}, Descriptions: map[string]string{}, Hidden: []string{}}
	data, err := os.ReadFile(filepath.Join(configDir, StoreFileName))
	if err != nil {
		return sd
	}
	var loaded storeData
	if json.Unmarshal(data, &loaded) != nil {
		return sd
	}
	if loaded.Folders != nil {
		sd.Folders = loaded.Folders
	}
	if loaded.Descriptions != nil {
		sd.Descriptions = loaded.Descriptions
	}
	if loaded.Hidden != nil {
		sd.Hidden = loaded.Hidden
	}
	return sd
}

// storeLock serialises read-modify-write cycles; writes go through a temp file and a rename so a
// crash or a second Bifrost window never leaves a half-written file.
var storeLock sync.Mutex

func saveStore(configDir string, sd storeData) error {
	data, err := json.MarshalIndent(sd, "", "    ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(configDir, StoreFileName+".*.tmp")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("writing %s: %w", StoreFileName, errFirst(werr, cerr))
	}
	if err := os.Rename(tmp.Name(), filepath.Join(configDir, StoreFileName)); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}

func errFirst(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// CheckFolder accepts only an absolute path of an existing directory, cleaned.
func CheckFolder(path string) (string, error) {
	if path == "" || !filepath.IsAbs(path) {
		return "", fmt.Errorf("folder must be an absolute path: %q", path)
	}
	clean := filepath.Clean(path)
	if !isDir(clean) {
		return "", fmt.Errorf("folder not found: %s", clean)
	}
	return clean, nil
}

// AddFolder remembers a directory so it is listed (and can start sessions) even with no sessions.
func AddFolder(configDir string, path string, label string) (string, error) {
	clean, err := CheckFolder(path)
	if err != nil {
		return "", err
	}
	storeLock.Lock()
	defer storeLock.Unlock()
	sd := loadStore(configDir)
	for i, f := range sd.Folders {
		if f.Path == clean {
			sd.Folders[i].Label = label
			return clean, saveStore(configDir, sd)
		}
	}
	sd.Folders = append(sd.Folders, ClaudeFolder{Path: clean, Label: label})
	return clean, saveStore(configDir, sd)
}

// RemoveFolder forgets a remembered directory; it never touches the directory itself.
func RemoveFolder(configDir string, path string) error {
	storeLock.Lock()
	defer storeLock.Unlock()
	sd := loadStore(configDir)
	kept := make([]ClaudeFolder, 0, len(sd.Folders))
	for _, f := range sd.Folders {
		if f.Path != filepath.Clean(path) {
			kept = append(kept, f)
		}
	}
	if len(kept) == len(sd.Folders) {
		return fmt.Errorf("folder is not remembered: %s", path)
	}
	sd.Folders = kept
	return saveStore(configDir, sd)
}

const maxDescriptionLen = 500

// SetDescription stores the user's own description of a session; an empty one removes it.
func SetDescription(configDir string, sessionId string, description string) error {
	if !IsSessionId(sessionId) {
		return fmt.Errorf("not a session id: %q", sessionId)
	}
	description = cleanText(description, maxDescriptionLen)
	storeLock.Lock()
	defer storeLock.Unlock()
	sd := loadStore(configDir)
	if description == "" {
		delete(sd.Descriptions, sessionId)
	} else {
		sd.Descriptions[sessionId] = description
	}
	return saveStore(configDir, sd)
}

// SetHidden removes a session from the list (or puts it back). Only the entry in Bifrost's own
// store changes; Claude's session files are never touched.
func SetHidden(configDir string, sessionId string, hidden bool) error {
	if !IsSessionId(sessionId) {
		return fmt.Errorf("not a session id: %q", sessionId)
	}
	storeLock.Lock()
	defer storeLock.Unlock()
	sd := loadStore(configDir)
	kept := make([]string, 0, len(sd.Hidden)+1)
	for _, id := range sd.Hidden {
		if id != sessionId {
			kept = append(kept, id)
		}
	}
	if hidden {
		kept = append(kept, sessionId)
	}
	sd.Hidden = kept
	return saveStore(configDir, sd)
}

// List returns every session of every harness plus the user's folders and descriptions.
func List(harnesses []Harness, configDir string) *ClaudeListResult {
	sd := loadStore(configDir)
	sessions := discoverAll(harnesses)
	hidden := make(map[string]bool, len(sd.Hidden))
	for _, id := range sd.Hidden {
		hidden[id] = true
	}
	for i := range sessions {
		sessions[i].Hidden = hidden[sessions[i].SessionId]
	}
	missing := []string{}
	seen := make(map[string]bool)
	check := func(cwd string) {
		if cwd == "" || seen[cwd] {
			return
		}
		seen[cwd] = true
		if !isDir(cwd) {
			missing = append(missing, cwd)
		}
	}
	for _, s := range sessions {
		check(s.Cwd)
	}
	for _, f := range sd.Folders {
		check(f.Path)
	}
	return &ClaudeListResult{Sessions: sessions, Folders: sd.Folders, Descriptions: sd.Descriptions, Missing: missing, Ts: time.Now().UnixMilli()}
}
