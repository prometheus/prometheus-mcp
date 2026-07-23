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
	"bytes"
	"fmt"
	"strings"
	"text/template"
)

// instructionsRunbook is one runbook inventory line for the instructions.
type instructionsRunbook struct {
	Name    string
	Trigger string
}

// instructionsTemplateData is the data the instructions template renders
// against.
type instructionsTemplateData struct {
	// Backend is the normalized (lowercased) `--prometheus.backend` value;
	// empty when unset.
	Backend string

	// TSDBAdminToolsEnabled mirrors `--dangerous.enable-tsdb-admin-tools`.
	// The admin tools are registered even when disabled (their handlers
	// reject calls), so tool presence can't stand in for the flag here.
	TSDBAdminToolsEnabled bool

	// Runbooks is the inventory rendered into the instructions, one
	// name + trigger line per embedded runbook; empty when the toolset
	// lacks the runbook tools.
	Runbooks []instructionsRunbook
}

// descriptionTrigger returns the leading sentence of a skill description;
// SKILL.md descriptions keep it short to serve as the inventory trigger line.
func descriptionTrigger(desc string) string {
	if first, _, found := strings.Cut(desc, ". "); found {
		return first + "."
	}
	return desc
}

// renderInstructions renders the embedded instructions template against the
// active configuration and toolset, so a narrowed toolset (`--mcp.tools`,
// backend overrides) never renders guidance for tools that aren't loaded.
func renderInstructions(cfg ServerConfig, toolset map[string]toolRegistration) (string, error) {
	raw, err := assets.ReadFile("assets/instructions.md.tmpl")
	if err != nil {
		return "", fmt.Errorf("failed to read instructions template from embedded assets: %w", err)
	}

	funcs := template.FuncMap{
		"hasTool": func(name string) bool {
			_, ok := toolset[name]
			return ok
		},
	}

	tmpl, err := template.New("instructions").Funcs(funcs).Parse(string(raw))
	if err != nil {
		return "", fmt.Errorf("failed to parse instructions template: %w", err)
	}

	data := instructionsTemplateData{
		Backend:               strings.ToLower(cfg.PrometheusBackend),
		TSDBAdminToolsEnabled: cfg.TSDBAdminToolsEnabled,
	}
	if _, ok := toolset["runbooks_list"]; ok {
		for _, def := range runbookDefs {
			data.Runbooks = append(data.Runbooks, instructionsRunbook{
				Name:    def.Name,
				Trigger: descriptionTrigger(def.Description),
			})
		}
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to render instructions template: %w", err)
	}

	return buf.String(), nil
}
