// Package addon is the declarative catalog of poolers, PostgreSQL extensions,
// embed toggles and UI front ends the stack already ships (design.md D-96).
// Nothing here invents a third-party install path the images do not contain.
// Engine names come only from the loaded descriptors (check 63 / R6).
package addon

import (
	"slices"
	"strings"

	"github.com/simtabi/dbpantry/src/internal/engine"
)

// Kind classifies an add-on for the wizard filters and Status card.
type Kind string

const (
	KindPooler    Kind = "pooler"
	KindExtension Kind = "extension"
	KindEmbed     Kind = "embed"
	KindUI        Kind = "ui"
)

// Spec is one catalog entry.
type Spec struct {
	ID           string // stable id, e.g. pooler.pg, ext.vector
	Title        string
	Kind         Kind
	Engines      []string // empty = all engines that match Kind rules
	Supported    bool
	NeedsPreload bool   // PG extension must be in shared_preload_libraries
	Profile      string // compose profile fragment, e.g. pg-pooler or ui
	ConfigKey    string // env key to toggle / merge
	Summary      string
	Rationale    string // why unsupported, when Supported is false
	DocsHint     string
}

// Catalog returns every known add-on. Pooler and UI rows are derived from the
// loaded engines; extension rows attach to every pgsql-driver engine.
func Catalog(engines []engine.Engine) []Spec {
	var out []Spec
	out = append(out, extensionSuite(engines)...)
	out = append(out, uiSuite(engines)...)
	out = append(out, embedSuite(engines)...)
	out = append(out, poolerSuite(engines)...)
	slices.SortFunc(out, func(a, b Spec) int {
		if a.Kind != b.Kind {
			return strings.Compare(string(a.Kind), string(b.Kind))
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out
}

// ForEngine filters the catalog to rows that apply to eng (and unsupported
// pooler stubs for engines that refuse external pooling).
func ForEngine(all []Spec, eng string) []Spec {
	var out []Spec
	for _, s := range all {
		if len(s.Engines) == 0 || slices.Contains(s.Engines, eng) {
			out = append(out, s)
		}
	}
	return out
}

// ByID finds one catalog entry.
func ByID(all []Spec, id string) (Spec, bool) {
	for _, s := range all {
		if s.ID == id {
			return s, true
		}
	}
	return Spec{}, false
}

func poolerSuite(engines []engine.Engine) []Spec {
	var out []Spec
	for _, e := range engines {
		id := "pooler." + e.Name
		switch {
		case e.Pooler != nil && e.Pooling == "external":
			out = append(out, Spec{
				ID:        id,
				Title:     poolerTitle(e),
				Kind:      KindPooler,
				Engines:   []string{e.Name},
				Supported: true,
				Profile:   e.Name + "-pooler",
				Summary:   "External pooler service (" + e.Pooler.Service + ") on the stack network.",
				DocsHint:  "docs/guides/pooling.md",
			})
		default:
			rationale := e.PoolingRationale
			if rationale == "" {
				rationale = "This engine has no external pooler in dbpantry."
			}
			out = append(out, Spec{
				ID:        id,
				Title:     "External pooler",
				Kind:      KindPooler,
				Engines:   []string{e.Name},
				Supported: false,
				Summary:   "Not offered for " + e.Title + ".",
				Rationale: rationale,
			})
		}
	}
	return out
}

// poolerTitle names the pooler from its image, never from an engine-name literal.
func poolerTitle(e engine.Engine) string {
	img := ""
	if e.Pooler != nil {
		img = strings.ToLower(e.Pooler.Image)
	}
	switch {
	case strings.Contains(img, "bouncer"):
		return "pgBouncer"
	case strings.Contains(img, "proxysql"):
		return "ProxySQL"
	default:
		return "Connection pooler"
	}
}

func enginesByDriver(engines []engine.Engine, driver string) []string {
	var out []string
	for _, e := range engines {
		if e.Driver == driver {
			out = append(out, e.Name)
		}
	}
	return out
}

func extensionSuite(engines []engine.Engine) []Spec {
	// Extensions ship only in the PostgreSQL image (driver pgsql).
	hosts := enginesByDriver(engines, "pgsql")
	if len(hosts) == 0 {
		return nil
	}
	type row struct {
		id, title, summary string
		preload            bool
	}
	rows := []row{
		{"vector", "pgvector", "Vector similarity search.", false},
		{"postgis", "PostGIS", "Geographic objects and spatial indexes.", false},
		{"pg_cron", "pg_cron", "Job scheduler inside PostgreSQL.", true},
		{"pgaudit", "pgaudit", "Session and object audit logging.", true},
		{"pg_stat_statements", "pg_stat_statements", "Track planning and execution statistics.", true},
		{"pg_trgm", "pg_trgm", "Trigram text similarity.", false},
		{"uuid-ossp", "uuid-ossp", "UUID generation functions.", false},
		{"pgcrypto", "pgcrypto", "Cryptographic functions.", false},
		{"citext", "citext", "Case-insensitive text type.", false},
		{"hstore", "hstore", "Key/value store type.", false},
		{"pg_repack", "pg_repack", "Online table reorganization.", false},
		{"pg_partman", "pg_partman", "Partition management.", false},
		{"pgtap", "pgtap", "Unit testing for PostgreSQL.", false},
		{"http", "http", "HTTP client from SQL.", false},
		{"pgjwt", "pgjwt", "JWT helpers (vendored SQL).", false},
		{"pgsodium", "pgsodium", "Modern cryptography via libsodium.", false},
		{"pg_net", "pg_net", "Async HTTP from the database.", true},
		{"pg_graphql", "pg_graphql", "GraphQL support.", false},
	}
	out := make([]Spec, 0, len(rows))
	for _, r := range rows {
		out = append(out, Spec{
			ID:           "ext." + r.id,
			Title:        r.title,
			Kind:         KindExtension,
			Engines:      append([]string(nil), hosts...),
			Supported:    true,
			NeedsPreload: r.preload,
			ConfigKey:    "MDB_PG_INIT_EXTENSIONS",
			Summary:      r.summary,
			DocsHint:     "docs/reference/spec.md §5",
		})
	}
	return out
}

func uiSuite(engines []engine.Engine) []Spec {
	// Driver/family tokens that are not engine names (check 63): "pgsql" is
	// fine; the MySQL wire protocol is detected via Driver prefix, not a name literal.
	pgsql := enginesByDriver(engines, "pgsql")
	wireMy := enginesMySQLProtocol(engines)
	sql := append(append([]string{}, pgsql...), wireMy...)
	var out []Spec
	if len(sql) > 0 {
		out = append(out, Spec{ID: "ui.adminer", Title: "Adminer", Kind: KindUI, Engines: sql, Supported: true, Profile: "ui", Summary: "Lightweight multi-engine SQL UI behind Caddy.", DocsHint: "docs/guides/dashboard.md"})
	}
	if len(pgsql) > 0 {
		out = append(out, Spec{ID: "ui.pgadmin", Title: "pgAdmin", Kind: KindUI, Engines: append([]string(nil), pgsql...), Supported: true, Profile: "ui", Summary: "Full PostgreSQL administration UI.", DocsHint: "docs/guides/dashboard.md"})
	}
	if len(wireMy) > 0 {
		out = append(out, Spec{ID: "ui.phpmyadmin", Title: "phpMyAdmin", Kind: KindUI, Engines: append([]string(nil), wireMy...), Supported: true, Profile: "ui", Summary: "MySQL/MariaDB web UI.", DocsHint: "docs/guides/dashboard.md"})
	}
	return out
}

// enginesMySQLProtocol lists engines that speak the MySQL wire protocol,
// without naming an engine (check 63 / R6). ProxySQL poolers and drivers
// whose name starts with "my" (descriptor MDB_ENGINE_DRIVER) both qualify.
func enginesMySQLProtocol(engines []engine.Engine) []string {
	var out []string
	seen := map[string]bool{}
	for _, e := range engines {
		ok := false
		if e.Pooler != nil && strings.Contains(strings.ToLower(e.Pooler.Image), "proxysql") {
			ok = true
		}
		if e.Family != "" && e.Family == e.Driver && strings.HasPrefix(e.Driver, "my") {
			ok = true
		}
		if !ok || seen[e.Name] {
			continue
		}
		seen[e.Name] = true
		out = append(out, e.Name)
	}
	return out
}

func embedSuite(engines []engine.Engine) []Spec {
	var out []Spec
	for _, e := range engines {
		if !e.Implemented {
			continue
		}
		up := strings.ToUpper(strings.ReplaceAll(e.Name, "-", "_"))
		out = append(out,
			Spec{ID: "embed.exporter." + e.Name, Title: e.Title + " metrics exporter", Kind: KindEmbed, Engines: []string{e.Name}, Supported: true, ConfigKey: "MDB_" + up + "_EMBED_EXPORTER", Summary: "Run the metrics exporter inside the engine container (standalone docker run).", DocsHint: "docs/reference/spec.md §6.1"},
			Spec{ID: "embed.backup." + e.Name, Title: e.Title + " embedded backup", Kind: KindEmbed, Engines: []string{e.Name}, Supported: true, ConfigKey: "MDB_" + up + "_EMBED_BACKUP", Summary: "Scheduled dumps inside the engine container when not using the backup profile.", DocsHint: "docs/reference/spec.md §6.1"},
		)
	}
	return out
}
