// Package stackview turns Docker Compose's machine-readable progress
// (--progress json, set with COMPOSE_PROGRESS=json) into a tree grouped by
// kind: networks, volumes, images, containers. Each image that is built shows
// a progress bar over the build steps BuildKit has reported so far.
//
// The stream is one JSON object per line (docker/compose cmd/display/json.go):
// id, parent_id, status (Working, Done, Warning or Error), text, details,
// current, total and percent. Compose documents its status texts ("Created",
// "Healthy", "Built", ...) as stable identifiers. A line that is not such an
// object is kept as log output: podman-compose and nerdctl compose have no
// JSON mode, and a script may print a line of its own.
package stackview

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/simtabi/dbpantry/src/internal/ui"
)

// event is one line of compose's JSON progress.
type event struct {
	ID       string `json:"id"`
	ParentID string `json:"parent_id"`
	Status   string `json:"status"`
	Text     string `json:"text"`
	Details  string `json:"details"`
	Current  int64  `json:"current"`
	Total    int64  `json:"total"`
	Percent  int    `json:"percent"`
}

// kinds are the categories, in the order they are drawn.
var kinds = []string{"Network", "Volume", "Image", "Container"}

var kindTitle = map[string]string{
	"Network": "Networks", "Volume": "Volumes", "Image": "Images", "Container": "Containers", "": "Other",
}

type resource struct {
	id, parent, kind, name string
	state                  ui.State
	text, details          string
	current, total         int64
	started, ended         time.Time
	order                  int
}

// View is the state of one compose run. It is safe for one writer (Feed)
// and one reader (Nodes) at a time.
type View struct {
	mu    sync.Mutex
	res   map[string]*resource
	n     int
	log   []string
	clock func() time.Time
}

// New returns an empty view.
func New() *View { return &View{res: map[string]*resource{}, clock: time.Now} }

// Feed takes one line of compose's output.
func (v *View) Feed(line string) {
	line = strings.TrimRight(line, "\r\n")
	if strings.TrimSpace(line) == "" {
		return
	}
	var e event
	if !strings.HasPrefix(line, "{") || json.Unmarshal([]byte(line), &e) != nil || e.ID == "" {
		v.mu.Lock()
		v.log = append(v.log, line)
		v.mu.Unlock()
		return
	}
	v.apply(e)
}

func (v *View) apply(e event) {
	v.mu.Lock()
	defer v.mu.Unlock()
	now := v.clock()
	r, ok := v.res[e.ID]
	if !ok {
		kind, name := split(e.ID)
		r = &resource{id: e.ID, parent: e.ParentID, kind: kind, name: name, started: now, order: v.n}
		v.n++
		v.res[e.ID] = r
	}
	if e.ParentID != "" {
		r.parent = e.ParentID
	}
	r.text, r.details = e.Text, e.Details
	r.current, r.total = e.Current, e.Total
	switch e.Status {
	case "Done":
		r.state, r.ended = ui.Done, now
	case "Warning":
		r.state = ui.Warning
	case "Error":
		r.state, r.ended = ui.Failed, now
	default:
		r.state, r.ended = ui.Running, time.Time{}
	}
}

// split reads "Container mdb-pg-1" as the kind and the name.
func split(id string) (kind, name string) {
	for _, k := range kinds {
		if strings.HasPrefix(id, k+" ") {
			return k, strings.TrimPrefix(id, k+" ")
		}
	}
	return "", id
}

// Log is every line that was not a progress event.
func (v *View) Log() []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return append([]string(nil), v.log...)
}

// Overall is how many top-level resources have finished, of those seen.
func (v *View) Overall(started time.Time) ui.Progress {
	v.mu.Lock()
	defer v.mu.Unlock()
	p := ui.Progress{Started: started}
	for _, r := range v.res {
		if r.parent != "" {
			continue
		}
		p.Total++
		if r.state == ui.Done {
			p.Done++
		}
	}
	return p
}

// Failed reports whether any resource ended in an error.
func (v *View) Failed() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, r := range v.res {
		if r.state == ui.Failed {
			return true
		}
	}
	return false
}

// Nodes draws the resources as one node per kind, each holding its
// resources in the order compose first reported them.
func (v *View) Nodes() []*ui.Node {
	v.mu.Lock()
	defer v.mu.Unlock()
	children := map[string][]*resource{}
	byKind := map[string][]*resource{}
	for _, r := range v.res {
		if r.parent != "" {
			if _, ok := v.res[r.parent]; ok {
				children[r.parent] = append(children[r.parent], r)
				continue
			}
		}
		byKind[r.kind] = append(byKind[r.kind], r)
	}
	var out []*ui.Node
	for _, k := range append(append([]string{}, kinds...), "") {
		rs := byKind[k]
		if len(rs) == 0 {
			continue
		}
		sortByOrder(rs)
		group := &ui.Node{Label: kindTitle[k], State: groupState(rs)}
		for _, r := range rs {
			group.Children = append(group.Children, v.node(r, children[r.id]))
		}
		group.Detail = countDetail(rs)
		out = append(out, group)
	}
	return out
}

func (v *View) node(r *resource, kids []*resource) *ui.Node {
	n := &ui.Node{Label: r.name, State: r.state, Detail: detail(r), Started: r.started, Ended: r.ended}
	switch {
	case r.state != ui.Running:
	case r.total > 0:
		n.Progress = &ui.Progress{Done: r.current, Total: r.total, Bytes: true, Started: r.started}
	case len(kids) > 0:
		// A build: its steps are its children. The bar counts steps BuildKit
		// has finished of those it has reported, which grows as it goes.
		p := &ui.Progress{Total: int64(len(kids)), Started: r.started}
		var running []*resource
		for _, c := range kids {
			switch c.state {
			case ui.Done:
				p.Done++
			case ui.Running:
				running = append(running, c)
			}
		}
		n.Progress = p
		sortByOrder(running)
		if len(running) > 2 {
			running = running[len(running)-2:]
		}
		for _, c := range running {
			n.Log = append(n.Log, c.name)
		}
	}
	return n
}

func detail(r *resource) string {
	d := r.text
	if r.details != "" && r.state != ui.Done {
		if d != "" {
			d += ": "
		}
		d += r.details
	}
	return d
}

func groupState(rs []*resource) ui.State {
	st := ui.Done
	for _, r := range rs {
		switch {
		case r.state == ui.Failed:
			return ui.Failed
		case r.state == ui.Running:
			st = ui.Running
		case r.state == ui.Warning && st == ui.Done:
			st = ui.Warning
		}
	}
	return st
}

func countDetail(rs []*resource) string {
	done := 0
	for _, r := range rs {
		if r.state == ui.Done {
			done++
		}
	}
	if done == len(rs) {
		return ""
	}
	return strconv.Itoa(done) + "/" + strconv.Itoa(len(rs))
}

func sortByOrder(rs []*resource) {
	sort.Slice(rs, func(i, j int) bool { return rs[i].order < rs[j].order })
}
