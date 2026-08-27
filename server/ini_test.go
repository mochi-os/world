// Mochi world: configuration parsing
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package main

import (
	"os"
	"testing"
)

// TestBooleanFallsBackOnRubbish — ini_int warns on a value it cannot read and
// keeps its default; ini_bool's default branch silently answered false, so
// `public = ture` quietly unpublished the server with nothing said anywhere.
func TestBooleanFallsBackOnRubbish(t *testing.T) {
	os.Setenv("MOCHI_WORLD_PUBLIC", "ture")
	t.Cleanup(func() { os.Unsetenv("MOCHI_WORLD_PUBLIC") })

	if !ini_bool("world", "public", true) {
		t.Error("a typo overrode a true default with false")
	}
	if ini_bool("world", "public", false) {
		t.Error("a typo overrode a false default with true")
	}
}

// TestBooleanReadsBothSpellings is the control: the fallback must not have
// swallowed the values that genuinely mean false.
func TestBooleanReadsBothSpellings(t *testing.T) {
	t.Cleanup(func() { os.Unsetenv("MOCHI_WORLD_PUBLIC") })
	for _, word := range []string{"false", "no", "off", "0", "FALSE", "Off"} {
		os.Setenv("MOCHI_WORLD_PUBLIC", word)
		if ini_bool("world", "public", true) {
			t.Errorf("%q did not read as false", word)
		}
	}
	for _, word := range []string{"true", "yes", "on", "1", "TRUE", "On"} {
		os.Setenv("MOCHI_WORLD_PUBLIC", word)
		if !ini_bool("world", "public", false) {
			t.Errorf("%q did not read as true", word)
		}
	}
	os.Unsetenv("MOCHI_WORLD_PUBLIC")
	if !ini_bool("world", "public", true) || ini_bool("world", "public", false) {
		t.Error("an unset key did not fall through to its default")
	}
}
