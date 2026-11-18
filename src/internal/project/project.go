// Package project reads what scripts/new-project prints with --format json:
// one object describing a project on one engine. The CLI draws it as a tree
// and tables (project new); the dashboard shows it on the project's page
// (with --show, which provisions nothing).
package project

import (
	"bytes"
	"encoding/json"
	"errors"
)

// Provisioned is one project on one engine.
type Provisioned struct {
	Engine     string   `json:"engine"`
	Title      string   `json:"title"`
	Database   string   `json:"database"`
	Owner      string   `json:"owner"`
	Readonly   string   `json:"readonly"`
	Extensions []string `json:"extensions"`
	Secrets    []Secret `json:"secrets"`
	TLSAlways  bool     `json:"tls_always"`
	Pooler     *Pooler  `json:"pooler"`
	Network    string   `json:"network"`
	// Block is the paste-ready .env block, password included.
	Block string `json:"block"`
}

// Secret is a role's password file and what the run did with it:
// generated, reused, or (under --show) existing or missing.
type Secret struct {
	Role   string `json:"role"`
	File   string `json:"file"`
	Action string `json:"action"`
}

// Pooler is the engine's pooler endpoint on the stack's network.
type Pooler struct {
	Host string `json:"host"`
	Port string `json:"port"`
}

// Parse reads the object from the script's standard output: its last
// non-empty line, after anything a hook printed despite the redirect.
func Parse(stdout []byte) (Provisioned, error) {
	var p Provisioned
	lines := bytes.Split(bytes.TrimSpace(stdout), []byte("\n"))
	if len(lines) == 0 || len(lines[len(lines)-1]) == 0 {
		return p, errors.New("scripts/new-project printed nothing")
	}
	err := json.Unmarshal(lines[len(lines)-1], &p)
	return p, err
}
