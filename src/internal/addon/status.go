package addon

import (
	"slices"
	"strings"
)

// Status is one catalog row with its effective on/off (and running) state.
type Status struct {
	Spec
	Engine        string   `json:"engine"`
	EngineEnabled bool     `json:"engine_enabled"`
	Enabled       bool     `json:"enabled"`
	Running       bool     `json:"running,omitempty"`
	ActiveOn      []string `json:"active_on,omitempty"` // project DBs that have an extension
}

// Profiles is the current MDB_PROFILES / COMPOSE_PROFILES list.
type Profiles []string

func ParseProfiles(s string) Profiles {
	var out Profiles
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (p Profiles) Has(name string) bool {
	return slices.Contains(p, name)
}

// Evaluate builds Status rows for the wizard and Status page.
// initExt is MDB_PG_INIT_EXTENSIONS; projectExt maps project name → extensions;
// up reports whether a compose service is running; engEnabled whether the
// engine profile is selected.
func Evaluate(
	catalog []Spec,
	profiles Profiles,
	initExt []string,
	preload []string,
	projectExt map[string][]string,
	engEnabled map[string]bool,
	up func(service string) bool,
	envTruthy func(key string) bool,
) []Status {
	var out []Status
	for _, spec := range catalog {
		engines := spec.Engines
		if len(engines) == 0 {
			continue
		}
		for _, eng := range engines {
			st := Status{Spec: spec, Engine: eng, EngineEnabled: engEnabled[eng]}
			switch spec.Kind {
			case KindPooler:
				st.Enabled = profiles.Has(spec.Profile) || profiles.Has("pooler")
				if st.Enabled && up != nil {
					// Legacy HA compose names the bouncer eng+"bouncer" (e.g. pgbouncer).
					st.Running = up(eng+"-pooler") || up(eng+"bouncer")
				}
			case KindExtension:
				name := strings.TrimPrefix(spec.ID, "ext.")
				st.Enabled = slices.Contains(initExt, name) || slices.Contains(preload, name)
				for proj, exts := range projectExt {
					if slices.Contains(exts, name) {
						st.ActiveOn = append(st.ActiveOn, proj)
					}
				}
				slices.Sort(st.ActiveOn)
			case KindUI:
				st.Enabled = profiles.Has("ui") || profiles.Has(spec.Profile)
			case KindEmbed:
				if spec.ConfigKey != "" && envTruthy != nil {
					st.Enabled = envTruthy(spec.ConfigKey)
				}
			}
			out = append(out, st)
		}
	}
	return out
}

// ForStatusPage keeps only supported add-ons on enabled engines (D-96).
func ForStatusPage(rows []Status) []Status {
	var out []Status
	for _, r := range rows {
		if r.Supported && r.EngineEnabled {
			out = append(out, r)
		}
	}
	return out
}

// KindGroup is one kind bucket inside an engine (pooler / extension / …).
type KindGroup struct {
	Kind  Kind     `json:"kind"`
	Label string   `json:"label"`
	Rows  []Status `json:"rows"`
}

// EngineGroup is the Status / Manage card section for one database engine.
type EngineGroup struct {
	Engine string      `json:"engine"`
	Title  string      `json:"title"`
	On     int         `json:"on"`
	Total  int         `json:"total"`
	Kinds  []KindGroup `json:"kinds"`
}

var kindOrder = []Kind{KindPooler, KindExtension, KindEmbed, KindUI}

// KindLabel is the human heading for a kind bucket.
func KindLabel(k Kind) string {
	switch k {
	case KindPooler:
		return "Poolers"
	case KindExtension:
		return "Extensions"
	case KindEmbed:
		return "Embed"
	case KindUI:
		return "Admin UIs"
	default:
		return string(k)
	}
}

// GroupByEngine buckets rows by engine, then by kind, for the Status and
// Manage cards (engine sections with on/off toggles).
func GroupByEngine(rows []Status, titleOf func(engine string) string) []EngineGroup {
	order := make([]string, 0)
	byEng := map[string][]Status{}
	for _, r := range rows {
		if _, ok := byEng[r.Engine]; !ok {
			order = append(order, r.Engine)
		}
		byEng[r.Engine] = append(byEng[r.Engine], r)
	}
	slices.Sort(order)
	out := make([]EngineGroup, 0, len(order))
	for _, eng := range order {
		list := byEng[eng]
		g := EngineGroup{Engine: eng, Total: len(list)}
		if titleOf != nil {
			g.Title = titleOf(eng)
		}
		if g.Title == "" {
			g.Title = eng
		}
		for _, k := range kindOrder {
			var bucket []Status
			for _, r := range list {
				if r.Kind == k {
					bucket = append(bucket, r)
					if r.Enabled {
						g.On++
					}
				}
			}
			if len(bucket) == 0 {
				continue
			}
			slices.SortFunc(bucket, func(a, b Status) int {
				return strings.Compare(a.Title, b.Title)
			})
			g.Kinds = append(g.Kinds, KindGroup{Kind: k, Label: KindLabel(k), Rows: bucket})
		}
		out = append(out, g)
	}
	return out
}

// MergeCSV merges a comma list with an item (enable) or removes it (disable).
func MergeCSV(current string, item string, enable bool) string {
	var parts []string
	seen := map[string]bool{}
	for _, p := range strings.Split(current, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !enable && p == item {
			continue
		}
		if !seen[p] {
			parts = append(parts, p)
			seen[p] = true
		}
	}
	if enable && !seen[item] {
		parts = append(parts, item)
	}
	return strings.Join(parts, ",")
}

// MergeProfiles adds or removes a profile token.
func MergeProfiles(current string, profile string, enable bool) string {
	return MergeCSV(current, profile, enable)
}
