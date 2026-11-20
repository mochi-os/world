// Mochi world: Configuration (INI + MOCHI_<SECTION>_<KEY> environment overrides)
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"

	goini "gopkg.in/ini.v1"
)

var ini_file *goini.File

// ini_load reads the configuration file. A missing file is not an error — an
// open world server runs usefully on defaults alone — but one that is there
// and cannot be read or parsed is: running on defaults then drops every
// setting the operator made at once, a bind restricted to one interface back
// to all of them and a certificate back to the ephemeral one, behind a
// process that looks healthy.
func ini_load(path string) error {
	file, err := goini.Load(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			info("configuration %s not found, using defaults", path)
			return nil
		}
		return fmt.Errorf("configuration %s: %w", path, err)
	}
	ini_file = file
	return nil
}

// ini_value returns the raw value for section/key, honouring the
// MOCHI_<SECTION>_<KEY> environment override, or "" when unset.
func ini_value(section string, key string) string {
	if v, found := os.LookupEnv("MOCHI_" + strings.ToUpper(section) + "_" + strings.ToUpper(key)); found {
		return v
	}
	if ini_file == nil {
		return ""
	}
	s := ini_file.Section(section)
	if s == nil || !s.HasKey(key) {
		return ""
	}
	return s.Key(key).String()
}

func ini_string(section string, key string, fallback string) string {
	v := ini_value(section, key)
	if v == "" {
		return fallback
	}
	return v
}

func ini_int(section string, key string, fallback int) int {
	v := ini_value(section, key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		warn("configuration %s.%s: not a number: %q", section, key, v)
		return fallback
	}
	return n
}

func ini_bool(section string, key string, fallback bool) bool {
	v := strings.ToLower(ini_value(section, key))
	switch v {
	case "":
		return fallback
	case "true", "yes", "on", "1":
		return true
	case "false", "no", "off", "0":
		return false
	default:
		// ini_int warns on a value it cannot read; silence here means a typo
		// like `public = ture` quietly unpublishes the server.
		warn("configuration %s.%s: not a boolean: %q", section, key, v)
		return fallback
	}
}
