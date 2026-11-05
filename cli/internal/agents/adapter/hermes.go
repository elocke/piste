// Portions ported from rulesync v22.0.0 (https://github.com/dyoshikawa/rulesync,
// commit 81b97709), src/utils/hermesagent.ts, src/constants/hermesagent-paths.ts.
// Copyright (c) 2024 dyoshikawa. MIT License; see cli/THIRD_PARTY_NOTICES.md.

package adapter

import (
	"fmt"
	"path/filepath"
	"regexp"
)

// HermesLayout is where a Hermes home keeps what the bundle renders.
// It is used for bundle rendering only: the engine never edits
// a live Hermes config; callers apply per-profile deltas separately.
type HermesLayout struct {
	Home string // HERMES_HOME, or ~/.hermes; passed explicitly (never read from the environment)
}

// ConfigFile is <home>/config.yaml (HERMESAGENT_CONFIG_FILE_NAME).
func (h HermesLayout) ConfigFile() string { return filepath.Join(h.Home, "config.yaml") }

// SkillsDir is <home>/skills; skills nest one level by category.
func (h HermesLayout) SkillsDir() string { return filepath.Join(h.Home, "skills") }

var profileRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// ProfileDir is <home>/profiles/<p>; the name is checked so it can't
// traverse (checkPathTraversal).
func (h HermesLayout) ProfileDir(p string) (string, error) {
	if !profileRe.MatchString(p) {
		return "", fmt.Errorf("hermes profile %q: want [a-z0-9][a-z0-9_-]*", p)
	}
	return filepath.Join(h.Home, "profiles", p), nil
}

// DefaultHermesHome is the global dir under a home directory.
func DefaultHermesHome(userHome string) string { return filepath.Join(userHome, ".hermes") }
