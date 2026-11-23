package project

import (
	"bytes"
	"encoding/json"
	"errors"
	"sort"
)

// Listing is what `scripts/project ls --format json` prints: the databases
// each running engine really has. It knows nothing of password files; Join
// puts the two together.
type Listing struct {
	Engines []EngineDatabases `json:"engines"`
}

// EngineDatabases is one running engine's databases. Reachable is false when
// the engine runs but did not answer, and then Databases says nothing.
type EngineDatabases struct {
	Engine    string     `json:"engine"`
	Title     string     `json:"title"`
	Reachable bool       `json:"reachable"`
	Databases []Database `json:"databases"`
}

// Database is one database and the extensions it has: nil when the engine's
// family cannot say, empty when it has none.
type Database struct {
	Name       string   `json:"name"`
	Extensions []string `json:"extensions"`
}

// ParseListing reads the object from the script's last non-empty line.
func ParseListing(stdout []byte) (Listing, error) {
	var l Listing
	lines := bytes.Split(bytes.TrimSpace(stdout), []byte("\n"))
	if len(lines) == 0 || len(lines[len(lines)-1]) == 0 {
		return l, errors.New("scripts/project ls printed nothing")
	}
	err := json.Unmarshal(lines[len(lines)-1], &l)
	return l, err
}

// State is what an engine says about a project dbpantry knows by its
// password file, or about a database it does not know.
type State string

const (
	// Active: the password file and the database both exist.
	Active State = "active"
	// Orphaned: the engine answers and has no such database; only the
	// password files are left (audit 2026-11-18, B1). `project drop` removes
	// them.
	Orphaned State = "orphaned"
	// Unknown: the engine is not running or did not answer.
	Unknown State = "unknown"
	// Unmanaged: a database with no password file, which dbpantry did not
	// provision and will not drop.
	Unmanaged State = "unmanaged"
)

// Known is a project dbpantry provisioned, found by its password file.
type Known struct {
	Engine string
	Name   string
}

// Entry is one project or database and its state. Extensions is nil when
// they are not known.
type Entry struct {
	Engine     string   `json:"engine"`
	Name       string   `json:"name"`
	State      State    `json:"state"`
	Extensions []string `json:"extensions"`
}

// systemDatabases are the databases an engine makes for itself, which are
// neither projects nor unmanaged. The families' listings leave out the rest.
var systemDatabases = map[string]bool{"postgres": true}

// Join puts the known projects and the listing together: each known project
// with its state, sorted as given, then the databases no password file names
// (Unmanaged), sorted by engine and name.
func (l Listing) Join(known []Known) []Entry {
	byEngine := map[string]EngineDatabases{}
	for _, e := range l.Engines {
		byEngine[e.Engine] = e
	}
	seen := map[Known]bool{}
	out := make([]Entry, 0, len(known))
	for _, k := range known {
		seen[k] = true
		e := Entry{Engine: k.Engine, Name: k.Name, State: Unknown}
		if eng, ok := byEngine[k.Engine]; ok && eng.Reachable {
			e.State = Orphaned
			for _, d := range eng.Databases {
				if d.Name == k.Name {
					e.State, e.Extensions = Active, d.Extensions
					break
				}
			}
		}
		out = append(out, e)
	}
	var extra []Entry
	for _, eng := range l.Engines {
		for _, d := range eng.Databases {
			if !seen[Known{eng.Engine, d.Name}] && !systemDatabases[d.Name] {
				extra = append(extra, Entry{Engine: eng.Engine, Name: d.Name, State: Unmanaged, Extensions: d.Extensions})
			}
		}
	}
	sort.Slice(extra, func(i, j int) bool {
		if extra[i].Engine != extra[j].Engine {
			return extra[i].Engine < extra[j].Engine
		}
		return extra[i].Name < extra[j].Name
	})
	return append(out, extra...)
}
