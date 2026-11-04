// Package endpoint answers "where do I connect": for every engine the stack
// can run, the address on the mdb network, the host port it is published on
// (if any), and the pooled address when the engine has a pooler.
//
// It reads the same settings the generators read and reproduces their
// arithmetic, so the published ports here are the ones scripts/gen-compose
// writes into compose.engines.yml (F9: the endpoints replace doing that
// arithmetic by hand).
package endpoint

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/simtabi/dbpantry/src/internal/config"
	"github.com/simtabi/dbpantry/src/internal/engine"
)

// SchemaVersion is the endpoints document's contract version; it moves with
// the manifest's rules (design.md D-68).
const SchemaVersion = 1

// Defaults, as .env.example and scripts/gen-compose state them.
const (
	DefaultPublish  = "none"
	DefaultPortBase = 54000
	DefaultBindAddr = "127.0.0.1"
	DefaultProfiles = "pg,ui"
	// PoolerPortStep is the offset scripts/gen-compose adds for a pooler's
	// published port: base + engine offset + 100.
	PoolerPortStep = 100
)

// Address is a host and port.
type Address struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

// Published is a port on the host, and what publishes it: the engine's own
// container ("direct") or the Caddy front door ("proxy").
type Published struct {
	Address string `json:"address"`
	Port    int    `json:"port"`
	Via     string `json:"via"`
}

// Access is one way to reach an engine.
type Access struct {
	// Network is the address on the mdb network, for a client that joins it.
	Network Address `json:"network"`
	// Published is nil when nothing is published (MDB_PUBLISH=none).
	Published *Published `json:"published"`
	// Superuser is whether the admin principal can authenticate this way.
	Superuser bool `json:"superuser"`
	// Enabled is whether the active profiles start the service behind it.
	Enabled bool `json:"enabled"`
}

// Endpoint is everything a client needs to connect to one engine.
type Endpoint struct {
	Engine     string             `json:"engine"`
	Title      string             `json:"title"`
	Family     string             `json:"family"`
	Enabled    bool               `json:"enabled"`
	TLSAlways  bool               `json:"tls_always"`
	BootBudget int                `json:"boot_budget_seconds"`
	Direct     Access             `json:"direct"`
	Pooled     *Access            `json:"pooled"`
	Principals []engine.Principal `json:"principals"`
}

// Document is the endpoints output.
type Document struct {
	SchemaVersion int        `json:"schema_version"`
	Publish       string     `json:"publish"`
	PortBase      int        `json:"port_base"`
	BindAddr      string     `json:"bind_addr"`
	Profiles      []string   `json:"profiles"`
	EnvFile       string     `json:"env_file"`
	Endpoints     []Endpoint `json:"endpoints"`
}

// Options select what to report.
type Options struct {
	// Profiles overrides the profile selection (a comma list, as PROFILES=).
	Profiles string
	// Project, when set, adds the app and read-only principals that
	// `make new-project NAME=<project>` creates.
	Project string
}

// Resolve builds the endpoints document for the implemented engines.
func Resolve(m engine.Manifest, s *config.Settings, o Options) (Document, error) {
	publish := s.File("MDB_PUBLISH")
	if publish == "" {
		publish = DefaultPublish
	}
	base := DefaultPortBase
	if v := s.File("MDB_PORT_BASE"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Document{}, fmt.Errorf("MDB_PORT_BASE=%q in %s is not a number", v, s.EnvFile)
		}
		base = n
	}
	bind := s.Effective("MDB_BIND_ADDR")
	if bind == "" {
		bind = DefaultBindAddr
	}
	profiles := Profiles(m, s, o.Profiles)

	doc := Document{
		SchemaVersion: SchemaVersion, Publish: publish, PortBase: base, BindAddr: bind,
		Profiles: profiles, EnvFile: s.EnvFile, Endpoints: []Endpoint{},
	}
	on := func(p string) bool { return contains(profiles, p) }

	for _, e := range m.Implemented() {
		upper := strings.ToUpper(strings.ReplaceAll(e.Name, "-", "_"))
		ep := Endpoint{
			Engine: e.Name, Title: e.Title, Family: e.Family, Enabled: on(e.Name),
			TLSAlways: e.TLSAlways, BootBudget: e.BootBudget,
			Direct:     Access{Network: Address{e.Name, e.Port}, Superuser: true, Enabled: on(e.Name)},
			Principals: append([]engine.Principal(nil), e.Principals...),
		}
		switch publish {
		case "direct":
			port, err := hostPort(s, "MDB_"+upper+"_HOST_PORT", base+e.PortOffset)
			if err != nil {
				return Document{}, err
			}
			ep.Direct.Published = &Published{Address: bind, Port: port, Via: "direct"}
		case "proxy":
			ep.Direct.Published = &Published{Address: bind, Port: base + e.PortOffset, Via: "proxy"}
		}
		if e.Pooler != nil {
			pooled := &Access{
				Network:   Address{e.Pooler.Service, e.Pooler.Port},
				Superuser: e.Pooler.Superuser,
				Enabled:   on(e.Pooler.Service),
			}
			// Only direct mode publishes a pooler; the proxy routes engines only.
			if publish == "direct" {
				port, err := hostPort(s, "MDB_"+upper+"_POOLER_HOST_PORT", base+e.PortOffset+PoolerPortStep)
				if err != nil {
					return Document{}, err
				}
				pooled.Published = &Published{Address: bind, Port: port, Via: "direct"}
			}
			ep.Pooled = pooled
		}
		if o.Project != "" {
			ep.Principals = append(ep.Principals, e.ProjectPrincipals(o.Project)...)
		}
		doc.Endpoints = append(doc.Endpoints, ep)
	}
	return doc, nil
}

// Profiles resolves the active profiles the way the Makefile does: PROFILES
// (or an explicit override), then MDB_PROFILES in the env file, then pg,ui;
// then `pooler` is expanded into <engine>-pooler for each selected engine
// that has one, as scripts/profiles does.
func Profiles(m engine.Manifest, s *config.Settings, override string) []string {
	raw := override
	if raw == "" {
		raw = s.Env("PROFILES")
	}
	if raw == "" {
		raw = s.File("MDB_PROFILES")
	}
	if raw == "" {
		raw = DefaultProfiles
	}
	in := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' })
	if !contains(in, "pooler") {
		return in
	}
	var out []string
	for _, p := range in {
		if p != "pooler" {
			out = append(out, p)
		}
	}
	for _, e := range m.Engines {
		if e.Pooler != nil && contains(in, e.Name) {
			out = append(out, e.Pooler.Service)
		}
	}
	return out
}

// hostPort is an explicit MDB_*_HOST_PORT (environment, then env file, as
// Compose interpolates it) or the base-plus-offset default.
func hostPort(s *config.Settings, key string, def int) (int, error) {
	v := s.Effective(key)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > 65535 {
		return 0, fmt.Errorf("%s=%q is not a port number", key, v)
	}
	return n, nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
