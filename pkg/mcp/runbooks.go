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
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// skillsAssetDir is the directory within the embedded assets FS that holds
// the runbooks, packaged as Agent Skills (one directory per skill containing
// a SKILL.md, per https://agentskills.io/specification and SEP-2640).
const skillsAssetDir = "assets/skills"

const (
	// skillsExtensionCapability is the MCP extension identifier declared
	// in the server capabilities to signal SEP-2640 skills support.
	skillsExtensionCapability = "io.modelcontextprotocol/skills"

	// skillsIndexURI is the well-known SEP-2640 skill discovery index URI.
	skillsIndexURI = "skill://index.json"

	// skillsIndexSchema is the discovery document schema the index follows.
	skillsIndexSchema = "https://schemas.agentskills.io/discovery/0.2.0/schema.json"
)

// skillURI returns the canonical skill:// resource URI for a skill's
// SKILL.md, per SEP-2640: skill://<skill-path>/SKILL.md.
func skillURI(name string) string {
	return "skill://" + name + "/SKILL.md"
}

// skillsIndex models the skill://index.json discovery document (SEP-2640).
type skillsIndex struct {
	Schema string             `json:"$schema"`
	Skills []skillsIndexEntry `json:"skills"`
}

type skillsIndexEntry struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
	URL         string `json:"url"`
}

// buildSkillsIndex renders the SEP-2640 discovery index for the embedded
// skills. The index is the authoritative record of which resources are
// skills.
func buildSkillsIndex() (string, error) {
	index := skillsIndex{Schema: skillsIndexSchema}
	for _, def := range runbookDefs {
		index.Skills = append(index.Skills, skillsIndexEntry{
			Name:        def.Name,
			Type:        "skill-md",
			Description: def.Description,
			URL:         skillURI(def.Name),
		})
	}

	out, err := json.Marshal(index)
	if err != nil {
		return "", fmt.Errorf("failed to marshal skills index: %w", err)
	}
	return string(out), nil
}

// skillNameRegexp enforces the Agent Skills `name` constraints: lowercase
// alphanumerics and single hyphens, no leading/trailing/consecutive hyphens.
var skillNameRegexp = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// skillFrontmatter is the YAML frontmatter of a SKILL.md file. Name and
// Description are required by the Agent Skills specification; the rest are
// optional and carried through for completeness.
type skillFrontmatter struct {
	Name          string `yaml:"name"`
	Description   string `yaml:"description"`
	License       string `yaml:"license,omitempty"`
	Compatibility string `yaml:"compatibility,omitempty"`
}

// runbookDef describes one embedded runbook: a guided workflow
// for a common Prometheus task, expressed in terms of this server's tools and
// packaged as an Agent Skill directory (assets/skills/<Name>/SKILL.md).
type runbookDef struct {
	// Name is the skill name from the SKILL.md frontmatter. It always
	// equals the skill directory name, and doubles as the MCP prompt name.
	Name string

	// Title is the human-readable runbook title, taken from the first
	// markdown H1 heading of the SKILL.md body.
	Title string

	// Description is the frontmatter description: what the runbook does
	// and when to use it, ending with task keywords for discovery.
	Description string

	// Raw is the complete SKILL.md content, frontmatter included. This is
	// what skill:// resources and the runbooks_read tool serve, so that
	// consumers see the skill exactly as it is on disk.
	Raw string

	// Body is the markdown content without the frontmatter block, used
	// where the YAML metadata would be noise (e.g. prompt messages).
	Body string
}

// runbookDefs is the registry of embedded runbooks, sorted by name. It is
// built at package init from the embedded assets; because the assets are
// compiled in, a failure to parse them is a programmer error and panics
// immediately (any test run or server start catches it).
var runbookDefs = mustLoadRunbookDefs()

func mustLoadRunbookDefs() []runbookDef {
	defs, err := loadRunbookDefs(assets)
	if err != nil {
		panic(fmt.Sprintf("invalid embedded skill assets: %v", err))
	}
	return defs
}

// loadRunbookDefs walks the embedded skills directory and parses one
// runbookDef per skill directory, validating the Agent Skills constraints
// that SEP-2640 relies on (name format, name==directory, no nested skills).
func loadRunbookDefs(fsys fs.FS) ([]runbookDef, error) {
	entries, err := fs.ReadDir(fsys, skillsAssetDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read skills asset dir: %w", err)
	}

	var defs []runbookDef
	for _, entry := range entries {
		if !entry.IsDir() {
			return nil, fmt.Errorf("unexpected non-directory entry %q in %s: skills must be directories containing a SKILL.md", entry.Name(), skillsAssetDir)
		}

		dir := entry.Name()
		skillDir := path.Join(skillsAssetDir, dir)
		raw, err := fs.ReadFile(fsys, path.Join(skillDir, "SKILL.md"))
		if err != nil {
			return nil, fmt.Errorf("skill %q is missing its SKILL.md: %w", dir, err)
		}

		def, err := parseSkill(string(raw))
		if err != nil {
			return nil, fmt.Errorf("skill %q: %w", dir, err)
		}
		if def.Name != dir {
			return nil, fmt.Errorf("skill %q: frontmatter name %q must match the directory name", dir, def.Name)
		}

		// Skills must not nest: no SKILL.md below the skill root.
		err = fs.WalkDir(fsys, skillDir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.Name() == "SKILL.md" && path.Dir(p) != skillDir {
				return fmt.Errorf("nested SKILL.md at %q: skills cannot contain other skills", p)
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("skill %q: %w", dir, err)
		}

		defs = append(defs, def)
	}

	sort.Slice(defs, func(i, j int) bool { return defs[i].Name < defs[j].Name })
	return defs, nil
}

// skillFrontmatterRegexp matches the YAML frontmatter block opening a
// SKILL.md document, capturing the YAML between the `---` fences.
// Regex is whitespace tolerant.
var skillFrontmatterRegexp = regexp.MustCompile(`\A---[ \t]*\r?\n((?s:.*?))\r?\n---[ \t]*\r?(?:\n|\z)`)

// parseSkill parses and validates a SKILL.md document.
func parseSkill(raw string) (runbookDef, error) {
	m := skillFrontmatterRegexp.FindStringSubmatchIndex(raw)
	if m == nil {
		return runbookDef{}, errors.New("SKILL.md must start with YAML frontmatter closed by a --- fence")
	}
	fmText := raw[m[2]:m[3]]
	body := raw[m[1]:]

	var fm skillFrontmatter
	if err := yaml.Unmarshal([]byte(fmText), &fm); err != nil {
		return runbookDef{}, fmt.Errorf("invalid frontmatter YAML: %w", err)
	}

	switch {
	case fm.Name == "" || len(fm.Name) > 64 || !skillNameRegexp.MatchString(fm.Name):
		return runbookDef{}, fmt.Errorf("frontmatter name %q must be 1-64 lowercase alphanumeric characters with single hyphens", fm.Name)
	case fm.Description == "" || len(fm.Description) > 1024:
		return runbookDef{}, errors.New("frontmatter description must be 1-1024 characters")
	}

	body = strings.TrimLeft(body, "\r\n")
	var title string
	for line := range strings.Lines(body) {
		if t, ok := strings.CutPrefix(line, "# "); ok {
			title = strings.TrimSpace(t)
			break
		}
	}
	if title == "" {
		return runbookDef{}, errors.New("SKILL.md body must contain an H1 title heading")
	}

	return runbookDef{
		Name:        fm.Name,
		Title:       title,
		Description: fm.Description,
		Raw:         raw,
		Body:        body,
	}, nil
}

// getRunbookDef returns the runbook definition with the given skill name.
func getRunbookDef(name string) (runbookDef, bool) {
	for _, def := range runbookDefs {
		if def.Name == name {
			return def, true
		}
	}
	return runbookDef{}, false
}

// listRunbooks renders a plain-text listing of all embedded runbooks, one per
// line as "<name>: <description>".
func listRunbooks() string {
	var sb strings.Builder
	for _, def := range runbookDefs {
		sb.WriteString(def.Name)
		sb.WriteString(": ")
		sb.WriteString(def.Description)
		sb.WriteString("\n")
	}
	return strings.TrimSuffix(sb.String(), "\n")
}

// getRunbookContent returns the full SKILL.md content of the named runbook.
// Names are validated against the registry, so path traversal is impossible.
func getRunbookContent(name string) (string, error) {
	def, ok := getRunbookDef(name)
	if !ok {
		return "", fmt.Errorf("unknown runbook %q; use runbooks_list to see available runbooks", name)
	}
	return def.Raw, nil
}
