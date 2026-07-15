// Copyright The Prometheus Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package mcp

import (
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"
)

// TestRunbookRegistryMatchesEmbeddedSkills asserts the parsed runbook
// registry and the embedded skill directories stay in sync, and that every
// skill satisfies the Agent Skills constraints SEP-2640 builds on.
func TestRunbookRegistryMatchesEmbeddedSkills(t *testing.T) {
	entries, err := fs.ReadDir(assets, skillsAssetDir)
	require.NoError(t, err)
	require.Len(t, runbookDefs, len(entries), "one registry entry per skill directory")

	names := make(map[string]bool)
	for _, def := range runbookDefs {
		require.False(t, names[def.Name], "duplicate skill name %q in registry", def.Name)
		names[def.Name] = true

		require.Regexp(t, skillNameRegexp, def.Name)
		require.LessOrEqual(t, len(def.Name), 64)
		require.NotEmpty(t, def.Title, "skill %q missing an H1 title", def.Name)
		require.NotEmpty(t, def.Description, "skill %q missing a description", def.Name)
		require.LessOrEqual(t, len(def.Description), 1024)

		// Descriptions should lead with a short trigger sentence; the
		// instructions template renders it, so keep it bounded.
		first, _, _ := strings.Cut(def.Description, ". ")
		require.LessOrEqual(t, len(first), 80, "skill %q: first description sentence is the instructions trigger and must stay short", def.Name)

		require.Regexp(t, skillFrontmatterRegexp, def.Raw, "skill %q Raw content should open with a frontmatter block", def.Name)
		require.NotContains(t, def.Body, "\nname: "+def.Name, "skill %q Body should not include frontmatter", def.Name)
		require.Contains(t, def.Body, "# "+def.Title)
	}

	for _, e := range entries {
		require.True(t, e.IsDir(), "unexpected non-directory in %s: %s", skillsAssetDir, e.Name())
		require.True(t, names[e.Name()], "embedded skill directory %q has no registry entry", e.Name())
	}
}

// TestLoadRunbookDefsValidation exercises the loader against malformed skill
// trees to make sure the Agent Skills constraints are actually enforced.
func TestLoadRunbookDefsValidation(t *testing.T) {
	valid := "---\nname: valid-skill\ndescription: A valid skill. Use for testing.\n---\n\n# Valid Skill\n\n## Steps\n\n1. Do the thing.\n"

	testCases := []struct {
		name        string
		fsys        fstest.MapFS
		errContains string
	}{
		{
			name: "valid skill parses",
			fsys: fstest.MapFS{
				skillsAssetDir + "/valid-skill/SKILL.md": &fstest.MapFile{Data: []byte(valid)},
			},
		},
		{
			name: "missing SKILL.md",
			fsys: fstest.MapFS{
				skillsAssetDir + "/empty-skill/README.md": &fstest.MapFile{Data: []byte("nope")},
			},
			errContains: "missing its SKILL.md",
		},
		{
			name: "name mismatch with directory",
			fsys: fstest.MapFS{
				skillsAssetDir + "/other-name/SKILL.md": &fstest.MapFile{Data: []byte(valid)},
			},
			errContains: "must match the directory name",
		},
		{
			name: "invalid name characters",
			fsys: fstest.MapFS{
				skillsAssetDir + "/Bad_Name/SKILL.md": &fstest.MapFile{Data: []byte("---\nname: Bad_Name\ndescription: Bad.\n---\n\n# Bad\n")},
			},
			errContains: "lowercase alphanumeric",
		},
		{
			name: "missing frontmatter",
			fsys: fstest.MapFS{
				skillsAssetDir + "/no-frontmatter/SKILL.md": &fstest.MapFile{Data: []byte("# Just Markdown\n")},
			},
			errContains: "must start with YAML frontmatter",
		},
		{
			name: "missing description",
			fsys: fstest.MapFS{
				skillsAssetDir + "/no-description/SKILL.md": &fstest.MapFile{Data: []byte("---\nname: no-description\n---\n\n# Title\n")},
			},
			errContains: "description must be",
		},
		{
			name: "missing H1 title",
			fsys: fstest.MapFS{
				skillsAssetDir + "/no-title/SKILL.md": &fstest.MapFile{Data: []byte("---\nname: no-title\ndescription: No title.\n---\n\njust text\n")},
			},
			errContains: "H1 title",
		},
		{
			name: "nested skills are rejected",
			fsys: fstest.MapFS{
				skillsAssetDir + "/valid-skill/SKILL.md":       &fstest.MapFile{Data: []byte(valid)},
				skillsAssetDir + "/valid-skill/inner/SKILL.md": &fstest.MapFile{Data: []byte(valid)},
			},
			errContains: "nested SKILL.md",
		},
		{
			name: "frontmatter fences tolerate trailing whitespace",
			fsys: fstest.MapFS{
				skillsAssetDir + "/ws-skill/SKILL.md": &fstest.MapFile{Data: []byte("---  \nname: ws-skill\ndescription: Whitespace. For testing.\n---\t\n\n# WS Skill\n")},
			},
		},
		{
			name: "CRLF line endings parse",
			fsys: fstest.MapFS{
				skillsAssetDir + "/crlf-skill/SKILL.md": &fstest.MapFile{Data: []byte("---\r\nname: crlf-skill\r\ndescription: CRLF. For testing.\r\n---\r\n\r\n# CRLF Skill\r\n")},
			},
		},
		{
			name: "unclosed frontmatter is rejected",
			fsys: fstest.MapFS{
				skillsAssetDir + "/unclosed/SKILL.md": &fstest.MapFile{Data: []byte("---\nname: unclosed\ndescription: Never closed.\n\n# Title\n")},
			},
			errContains: "must start with YAML frontmatter",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadRunbookDefs(tc.fsys)
			if tc.errContains == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.errContains)
			}
		})
	}
}

func TestListRunbooks(t *testing.T) {
	listing := listRunbooks()

	lines := strings.Split(listing, "\n")
	require.Len(t, lines, len(runbookDefs))

	for _, def := range runbookDefs {
		require.Contains(t, listing, def.Name+": "+def.Description)
	}
}

func TestGetRunbookContent(t *testing.T) {
	t.Run("known runbook returns full SKILL.md content", func(t *testing.T) {
		for _, def := range runbookDefs {
			content, err := getRunbookContent(def.Name)
			require.NoError(t, err, "runbook %q", def.Name)
			require.Regexp(t, skillFrontmatterRegexp, content, "runbook %q content should open with a frontmatter block", def.Name)
			require.Contains(t, content, "name: "+def.Name)
			require.Contains(t, content, "# "+def.Title)
			require.Contains(t, content, "## Getting oriented", "runbook %q should orient the reader on the relevant tools", def.Name)
			require.Contains(t, content, "## Topics worth exploring", "runbook %q should suggest exploration topics", def.Name)
		}
	})

	t.Run("unknown runbook returns error", func(t *testing.T) {
		_, err := getRunbookContent("nonexistent-skill")
		require.ErrorContains(t, err, "unknown runbook")
	})

	t.Run("path traversal is rejected", func(t *testing.T) {
		_, err := getRunbookContent("../instructions.md.tmpl")
		require.ErrorContains(t, err, "unknown runbook")
	})
}
