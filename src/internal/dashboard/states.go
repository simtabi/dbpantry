package dashboard

import (
	"fmt"
	"hash/fnv"
)

// State is one status the dashboard shows as a badge: the word on the badge,
// its colour, what it means and what to do about it. The badges' tooltips,
// the Help page's status guide and docs/guides/statuses.md all say the same
// thing because they all read this table (TestStatusGuideMatchesTheDocs).
type State struct {
	Key     string // the value the code compares (status, Health, State)
	Label   string // the word on the badge
	Tone    string // Tabler colour: green, yellow, azure, red, secondary
	Meaning string
	Next    string // what to do; empty when nothing is needed
}

// StateGroup is one family of badges, in the order the guide lists them.
type StateGroup struct {
	ID     string
	Title  string
	Where  string // where the badge appears
	States []State
}

// StateGuide is every status badge the dashboard draws.
var StateGuide = []StateGroup{
	{
		ID: "engine", Title: "Engines", Where: "Stack › By engine, Engines, an engine's page, Overview",
		States: []State{
			{Key: "running", Label: "running", Tone: "green",
				Meaning: "In MDB_PROFILES and its container is up, so it answers on the addresses its page lists.",
				Next:    ""},
			{Key: "stopped", Label: "stopped", Tone: "yellow",
				Meaning: "In MDB_PROFILES, but its container is not running: the stack is down, the container exited, or it has not started yet.",
				Next:    "Start the stack (dbpantry up). If it stops again, read dbpantry logs ENGINE."},
			{Key: "orphan", Label: "leftover", Tone: "azure",
				Meaning: "Its container is still running, but the engine is no longer in MDB_PROFILES: the profile was removed after it started. It still answers, but dbpantry up no longer manages it.",
				Next:    "Add it back to MDB_PROFILES, or stop it with dbpantry service stop ENGINE. Its data volume stays."},
			{Key: "off", Label: "disabled", Tone: "secondary",
				Meaning: "Not in MDB_PROFILES and no container is running. Nothing is lost: its data volume, if it ever had one, is kept.",
				Next:    "Use “Start with …” on its page, or add it to MDB_PROFILES and run dbpantry up."},
		},
	},
	{
		ID: "health", Title: "Service health", Where: "Services, Status, the header counters",
		States: []State{
			{Key: "healthy", Label: "healthy", Tone: "green",
				Meaning: "The container's health check passes."},
			{Key: "starting", Label: "starting", Tone: "yellow",
				Meaning: "The container is up and its health check has not passed yet. Engines get a boot budget (90s, or 300s for heavy ones) before this counts as a failure.",
				Next:    "Wait. If it stays here past the budget, read dbpantry logs SERVICE."},
			{Key: "unhealthy", Label: "unhealthy", Tone: "red",
				Meaning: "The health check keeps failing, so the service is up but not answering as it should.",
				Next:    "Read dbpantry logs SERVICE, then dbpantry restart SERVICE."},
			{Key: "none", Label: "no check", Tone: "azure",
				Meaning: "Running, and the image declares no health check, so running is all dbpantry can say."},
		},
	},
	{
		ID: "project", Title: "Projects", Where: "Projects, Databases & users, an engine's page",
		States: []State{
			{Key: "active", Label: "active", Tone: "green",
				Meaning: "The database exists and its password files are in secrets/."},
			{Key: "orphaned", Label: "orphaned", Tone: "yellow",
				Meaning: "Password files are in secrets/, but the engine has no such database: it was dropped outside dbpantry, or the volume was replaced.",
				Next:    "Provision it again (dbpantry project new) or drop the files (dbpantry project drop)."},
			{Key: "unmanaged", Label: "unmanaged", Tone: "secondary",
				Meaning: "The engine has this database, but no password file names it: dbpantry did not create it.",
				Next:    "Adopt it with dbpantry project new --name NAME to give it dbpantry's roles."},
			{Key: "unknown", Label: "unknown", Tone: "secondary",
				Meaning: "The engine is not running or did not answer, so dbpantry cannot tell whether the database exists.",
				Next:    "Start the engine; the state settles on the next refresh."},
		},
	},
	{
		ID: "job", Title: "Jobs", Where: "the job drawer, Jobs, Overview's recent activity",
		States: []State{
			{Key: "running", Label: "running", Tone: "yellow",
				Meaning: "The command is still running. One mutating job runs at a time."},
			{Key: "succeeded", Label: "succeeded", Tone: "green",
				Meaning: "The command exited 0."},
			{Key: "failed", Label: "failed", Tone: "red",
				Meaning: "The command exited non-zero; the badge shows the exit code and the drawer opens on the failure.",
				Next:    "Read the failure banner, fix the cause, then Retry."},
		},
	},
	{
		ID: "live", Title: "Live badge", Where: "the header, beside the history arrows",
		States: []State{
			{Key: "live", Label: "Live", Tone: "green",
				Meaning: "The page refreshes its data every 5 seconds."},
			{Key: "paused", Label: "Paused", Tone: "yellow",
				Meaning: "You paused it, or the page keeps a form or a password on screen and never refreshes on its own.",
				Next:    "Click the badge to resume, where the page allows it."},
			{Key: "stale", Label: "Retrying", Tone: "yellow",
				Meaning: "One or two refreshes failed in a row; it keeps trying."},
			{Key: "error", Label: "Disconnected", Tone: "red",
				Meaning: "Three refreshes failed in a row: the dashboard process stopped or the network dropped.",
				Next:    "Check the dbpantry dashboard process, then click the badge to retry."},
		},
	},
}

// chipTones is the Tabler palette chips draw from; neighbours in the list
// differ, so adjacent profiles stay apart.
var chipTones = []string{"blue", "orange", "teal", "purple", "lime", "pink", "cyan", "yellow", "indigo", "red", "green", "azure"}

// toneFor gives a name a fixed colour: the same profile is the same colour
// on every page and every refresh.
func toneFor(name string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	return chipTones[h.Sum32()%uint32(len(chipTones))] // #nosec G115 -- chipTones is a short fixed list
}

// toneMap colours a list of names: each starts from its own toneFor and
// moves to the next free colour on a clash, so no two chips in one list match.
func toneMap(names []string) map[string]string {
	out := make(map[string]string, len(names))
	used := map[string]bool{}
	for _, n := range names {
		h := fnv.New32a()
		_, _ = h.Write([]byte(n))
		start := int(h.Sum32() % uint32(len(chipTones))) // #nosec G115 -- chipTones is a short fixed list
		tone := chipTones[start]
		for i := 0; i < len(chipTones); i++ {
			if c := chipTones[(start+i)%len(chipTones)]; !used[c] {
				tone = c
				break
			}
		}
		used[tone] = true
		out[n] = tone
	}
	return out
}

// stateOf looks a badge up by group and key. An unknown key is drawn plainly
// with the key as its label, so a new state shows up rather than vanishing.
func stateOf(group, key string) State {
	for _, g := range StateGuide {
		if g.ID != group {
			continue
		}
		for _, s := range g.States {
			if s.Key == key {
				return s
			}
		}
	}
	return State{Key: key, Label: key, Tone: "secondary"}
}

// stateGroup returns one group of the guide by ID; an unknown ID is a
// template error rather than an empty legend.
func stateGroup(id string) (StateGroup, error) {
	for _, g := range StateGuide {
		if g.ID == id {
			return g, nil
		}
	}
	return StateGroup{}, fmt.Errorf("stateGroup: no group %q", id)
}
