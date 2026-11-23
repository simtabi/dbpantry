package stackview

import (
	"strings"
	"testing"
	"time"

	"github.com/simtabi/dbpantry/src/internal/ui"
)

// A run shaped like the one that prompted this package: two images that
// fail to resolve, are built, then two containers that become healthy.
var run = []string{
	`{"id":"Image localhost/dbpantry-pg:17","status":"Warning","text":"Pulling","details":"failed to resolve reference"}`,
	`{"id":"Image localhost/dbpantry-pg:17","status":"Working","text":"Building"}`,
	`{"id":"#1 [pg builder 1/10] FROM pgvector","parent_id":"Image localhost/dbpantry-pg:17","status":"Done"}`,
	`{"id":"#2 [pg builder 2/10] COPY fetch.sh","parent_id":"Image localhost/dbpantry-pg:17","status":"Done"}`,
	`{"id":"#3 [pg builder 3/10] RUN apt-get","parent_id":"Image localhost/dbpantry-pg:17","status":"Working"}`,
	`{"id":"#4 [pg builder 4/10] RUN make","parent_id":"Image localhost/dbpantry-pg:17","status":"Working"}`,
	`not json: a line podman-compose printed`,
	`{"id":"Network mdb_net","status":"Done","text":"Created"}`,
}

func TestBuildShowsStepProgressAndHidesTheResolvedWarning(t *testing.T) {
	v := New()
	for _, l := range run {
		v.Feed(l)
	}
	nodes := v.Nodes()
	if len(nodes) != 2 || nodes[0].Label != "Networks" || nodes[1].Label != "Images" {
		t.Fatalf("groups in kind order: Networks, Images; got %v", labels(nodes))
	}
	img := nodes[1].Children[0]
	if img.Label != "localhost/dbpantry-pg:17" || img.State != ui.Running || img.Detail != "Building" {
		t.Errorf("the image is building, not warning: %+v", img)
	}
	if img.Progress == nil || img.Progress.Done != 2 || img.Progress.Total != 4 {
		t.Fatalf("2 of 4 build steps done, got %+v", img.Progress)
	}
	if len(img.Log) != 2 || !strings.Contains(img.Log[1], "RUN make") {
		t.Errorf("the running steps are shown under the image: %v", img.Log)
	}
	if got := v.Log(); len(got) != 1 || !strings.Contains(got[0], "podman-compose") {
		t.Errorf("a line that is not an event is kept as log: %v", got)
	}
	if p := v.Overall(time.Now()); p.Done != 1 || p.Total != 2 {
		t.Errorf("1 of 2 top-level resources done, got %+v", p)
	}
}

func TestFinishedRunIsAllDone(t *testing.T) {
	v := New()
	for _, l := range run {
		v.Feed(l)
	}
	for _, l := range []string{
		`{"id":"Image localhost/dbpantry-pg:17","status":"Done","text":"Built"}`,
		`{"id":"Container mdb-pg-1","status":"Working","text":"Starting"}`,
		`{"id":"Container mdb-pg-1","status":"Done","text":"Healthy"}`,
	} {
		v.Feed(l)
	}
	for _, g := range v.Nodes() {
		if g.State != ui.Done || g.Detail != "" {
			t.Errorf("group %s: state %v, detail %q; want done with no count", g.Label, g.State, g.Detail)
		}
		for _, c := range g.Children {
			if c.Progress != nil {
				t.Errorf("%s finished but still shows a bar", c.Label)
			}
			if c.Detail != "" {
				t.Errorf("%s Done leaf still shows redundant status %q", c.Label, c.Detail)
			}
		}
	}
	if v.Failed() {
		t.Error("nothing failed")
	}
}

func TestErrorFailsTheGroup(t *testing.T) {
	v := New()
	v.Feed(`{"id":"Container mdb-pg-1","status":"Error","text":"Error","details":"dependency failed to start"}`)
	g := v.Nodes()[0]
	if g.State != ui.Failed || !v.Failed() {
		t.Fatalf("an errored container fails its group and the run")
	}
	if !strings.Contains(g.Children[0].Detail, "dependency failed") {
		t.Errorf("the error's details are shown: %q", g.Children[0].Detail)
	}
}

func TestDownloadUsesBytes(t *testing.T) {
	v := New()
	v.Feed(`{"id":"Image docker.io/library/caddy","status":"Working","text":"Downloading","current":1048576,"total":4194304}`)
	p := v.Nodes()[0].Children[0].Progress
	if p == nil || !p.Bytes || p.Fraction() != 0.25 {
		t.Errorf("a pull shows bytes, 25%%: %+v", p)
	}
}

func labels(ns []*ui.Node) []string {
	var out []string
	for _, n := range ns {
		out = append(out, n.Label)
	}
	return out
}
