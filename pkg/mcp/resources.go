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
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	resourcePrefix = "prometheus://"
)

// Resource definitions.
var (
	docsListResource = &mcp.Resource{
		URI:         resourcePrefix + "docs",
		Name:        "List of Official Prometheus Documentation Files",
		Description: "List of markdown files containing the official Prometheus documentation from the prometheus/docs repo",
		MIMEType:    "text/plain",
	}

	docsReadResourceTemplate = &mcp.ResourceTemplate{
		URITemplate: resourcePrefix + "docs/{+file}",
		Name:        "Official Prometheus Documentation",
		Description: "Read the named markdown file containing official Prometheus documentation from the prometheus/docs repo",
		MIMEType:    "text/markdown",
	}

	skillsIndexResource = &mcp.Resource{
		URI:         skillsIndexURI,
		Name:        "skills-index",
		Title:       "Agent Skills Discovery Index",
		Description: "SEP-2640 discovery index of the Agent Skills (runbooks) embedded in this server",
		MIMEType:    "application/json",
	}
)

// registerResources registers all MCP resources with the server.
func registerResources(server *mcp.Server, container *ServerContainer) {
	// Add static resources
	server.AddResource(docsListResource, container.DocsListResourceHandler)

	// Add resource templates for reading specific doc files
	server.AddResourceTemplate(docsReadResourceTemplate, container.DocsReadResourceHandler)

	// Expose the embedded runbooks as Agent Skills per SEP-2640: one
	// skill://<name>/SKILL.md resource per skill, plus the well-known
	// discovery index.
	for _, def := range runbookDefs {
		server.AddResource(&mcp.Resource{
			URI:         skillURI(def.Name),
			Name:        def.Name,
			Title:       def.Title,
			Description: def.Description,
			MIMEType:    "text/markdown",
		}, container.SkillReadResourceHandler)
	}
	server.AddResource(skillsIndexResource, container.SkillsIndexResourceHandler)
}

// Resource handlers

// DocsListResourceHandler handles the docs list resource request.
func (s *ServerContainer) DocsListResourceHandler(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	names, err := s.GetDocFileNames()
	if err != nil {
		return nil, fmt.Errorf("failed to list docs: %w", err)
	}

	return &mcp.ReadResourceResult{
		Contents: []*mcp.ResourceContents{
			{
				URI:      req.Params.URI,
				MIMEType: "text/plain",
				Text:     strings.Join(names, "\n"),
			},
		},
	}, nil
}

// SkillsIndexResourceHandler serves the SEP-2640 skill discovery index.
func (s *ServerContainer) SkillsIndexResourceHandler(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	index, err := buildSkillsIndex()
	if err != nil {
		return nil, err
	}

	return &mcp.ReadResourceResult{
		Contents: []*mcp.ResourceContents{
			{
				URI:      req.Params.URI,
				MIMEType: "application/json",
				Text:     index,
			},
		},
	}, nil
}

// SkillReadResourceHandler serves a runbook's SKILL.md content from its
// skill://<name>/SKILL.md resource URI.
func (s *ServerContainer) SkillReadResourceHandler(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	// Parse the URI to properly handle URL-encoded characters.
	u, err := url.Parse(req.Params.URI)
	if err != nil {
		return nil, fmt.Errorf("invalid resource URI: %w", err)
	}

	// Validate scheme.
	if u.Scheme != "skill" {
		return nil, fmt.Errorf("invalid skill resource URI scheme: %s", u.Scheme)
	}

	// Only single-file skills are served.
	if u.Path != "/SKILL.md" {
		return nil, fmt.Errorf("invalid skill resource path %q: only SKILL.md files are served", u.Path)
	}
	if u.Host == "" {
		return nil, errors.New("a skill name is required when reading skill resources")
	}

	content, err := getRunbookContent(u.Host)
	if err != nil {
		return nil, fmt.Errorf("failed to read runbook: %w", err)
	}

	return &mcp.ReadResourceResult{
		Contents: []*mcp.ResourceContents{
			{
				URI:      req.Params.URI,
				MIMEType: "text/markdown",
				Text:     content,
			},
		},
	}, nil
}

// DocsReadResourceHandler handles reading specific documentation files via the resource template.
func (s *ServerContainer) DocsReadResourceHandler(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	// Parse the URI to properly handle URL-encoded characters.
	u, err := url.Parse(req.Params.URI)
	if err != nil {
		return nil, fmt.Errorf("invalid resource URI: %w", err)
	}

	// Validate scheme.
	if u.Scheme != "prometheus" {
		return nil, fmt.Errorf("invalid docs resource URI scheme: %s", u.Scheme)
	}

	// Get file path. Ie, prometheus://docs/file/to-read.md -> file/to-read.md.
	filename := strings.TrimPrefix(u.Path, "/")
	if filename == "" {
		return nil, errors.New("at least 1 filename is required when requesting docs to read")
	}

	content, err := s.GetDocFileContent(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read file from docs: %w", err)
	}

	return &mcp.ReadResourceResult{
		Contents: []*mcp.ResourceContents{
			{
				URI:      req.Params.URI,
				MIMEType: "text/markdown",
				Text:     content,
			},
		},
	}, nil
}
