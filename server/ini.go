// Mochi world: Configuration (INI + MOCHI_<SECTION>_<KEY> environment overrides)
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package main

import (
	"os"
	"strconv"
	"strings"

	goini "gopkg.in/ini.v1"
)

var ini_file *goini.File

// ini_load reads the configuration file. A missing file is not an error — an
// open world server runs usefully on defaults alone.
func ini_load(path string) {
	file, err := goini.Load(path)
	if err != nil {
		if os.IsNotExist(err) {
			info("configuration %s not found, using defaults", path)
			return
		}
		warn("configuration %s: %v", path, err)
		return
	}
	ini_file = file
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
	default:
		return false
	}
}
