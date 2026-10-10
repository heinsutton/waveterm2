// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package shellexec

import (
	"os"
	"testing"
)

func TestPathWithDir(t *testing.T) {
	sep := string(os.PathListSeparator)
	if got := pathWithDir("/usr/bin"+sep+"/bin", "/w/bin"); got != "/w/bin"+sep+"/usr/bin"+sep+"/bin" {
		t.Errorf("prepend: %q", got)
	}
	if got := pathWithDir("/w/bin"+sep+"/bin", "/w/bin"); got != "/w/bin"+sep+"/bin" {
		t.Errorf("already first: %q", got)
	}
	if got := pathWithDir("", "/w/bin"); got != "/w/bin" {
		t.Errorf("empty: %q", got)
	}
}
