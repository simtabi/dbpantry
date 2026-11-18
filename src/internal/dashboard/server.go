package dashboard

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"html/template"
	"io/fs"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/simtabi/dbpantry/src/internal/endpoint"
	"github.com/simtabi/dbpantry/src/internal/project"
)

//go:embed templates/*.html resources/assets
var files embed.FS

// Sources read the stack. Snapshot is called once per page and per
// refresh. Project prints one project's connection block, password
// included, from its password files (scripts/new-project --show); it is
// called only for that project's page.
type Sources struct {
	Snapshot func(context.Context) (Snapshot, error)
	Project  func(ctx context.Context, engine, name string) (project.Provisioned, error)
}

// ErrNoProject is what Sources.Project returns for a project that does not
// exist, so the page answers 404 rather than 500.
var ErrNoProject = errors.New("no such project")

// pages are the dashboard's pages, each a template of its own over the
// shared layout. The key is the page's name; the menu marks it active.
var pages = []string{"overview", "services", "images", "engines", "engine", "projects", "project", "backups", "connect", "error"}

// fresh is how old a reading of the stack may be before the next request
// starts a new one. Reading the stack runs the runtime's CLI several times
// and takes seconds on Docker Desktop and OrbStack (measured 3.7-5.5 s,
// 2026-11-18), longer than the five-second refresh can wait for.
const fresh = 2 * time.Second

// page is what every page template is given.
type page struct {
	Page  string
	Title string
	S     Snapshot
	// Engine is the engine page's engine.
	Engine *endpoint.Endpoint
	// Project is the project page's project.
	Project *project.Provisioned
	// Refresh is false on a page that must not reload itself (a project's
	// page, whose password the reader may have revealed).
	Refresh bool
	// Code and Message describe the error page's error.
	Code    int
	Message string
}

// cached serves the latest reading at once and, when it is older than
// fresh, starts one new reading in the background for the requests after
// it (stale-while-revalidate). Only the first request, with no reading yet,
// waits. The footer shows the reading's time, so its age is never hidden.
// A failed reading is not kept: the last good one stays, and the next
// request tries again; with no good reading the error is returned.
func cached(read func(context.Context) (Snapshot, error), now func() time.Time) func(context.Context) (Snapshot, error) {
	var mu sync.Mutex
	var last Snapshot
	var at time.Time
	var running bool
	refresh := func(ctx context.Context) (Snapshot, error) {
		s, err := read(ctx)
		mu.Lock()
		defer mu.Unlock()
		running = false
		if err == nil {
			last, at = s, now()
		}
		return s, err
	}
	return func(ctx context.Context) (Snapshot, error) {
		mu.Lock()
		if at.IsZero() {
			mu.Unlock()
			return refresh(ctx)
		}
		s := last
		if now().Sub(at) >= fresh && !running {
			running = true
			// The reading outlives the request that started it, so it keeps
			// the request's values but not its cancellation, and gets a
			// deadline of its own.
			bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			go func() {
				defer cancel()
				_, _ = refresh(bg)
			}()
		}
		mu.Unlock()
		return s, nil
	}
}

// Handler serves the dashboard:
//
//	GET /                          overview
//	GET /services, /images, /engines, /projects, /backups, /connect
//	GET /engines/{engine}          one engine
//	GET /projects/{engine}/{name}  one project, with its .env block
//	GET /api/status                the snapshot as JSON (no passwords)
//	GET /assets/<kind>/…           stylesheets, scripts, favicon, embedded
//
// A request with "X-Partial: 1" gets the page's content alone, which the
// page fetches every five seconds to refresh itself.
//
// When allowedHosts is set, a request whose Host is not one of them is
// refused, so a web page elsewhere cannot read the dashboard through a DNS
// name that resolves to this machine (DNS rebinding). A project page, which
// carries a password, is also refused to a cross-site request.
func Handler(src Sources, allowedHosts []string) (http.Handler, error) {
	sets := map[string]*template.Template{}
	for _, name := range pages {
		t, err := template.New("").Funcs(funcs).ParseFS(files, "templates/layout.html", "templates/"+name+".html")
		if err != nil {
			return nil, fmt.Errorf("template %s: %w", name, err)
		}
		sets[name] = t
	}
	assets, err := fs.Sub(files, "resources/assets")
	if err != nil {
		return nil, err
	}
	readStack := cached(src.Snapshot, time.Now)

	render := func(w http.ResponseWriter, r *http.Request, p page) {
		var buf bytes.Buffer
		name := "layout"
		if r.Header.Get("X-Partial") == "1" {
			name = "content"
		}
		if err := sets[p.Page].ExecuteTemplate(&buf, name, p); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// The footer's "read at" sits outside what a refresh replaces, so
		// the page reads the reading's time from here.
		if !p.S.Generated.IsZero() {
			w.Header().Set("X-Generated", p.S.Generated.Format("15:04:05"))
		}
		if p.Code != 0 {
			w.WriteHeader(p.Code)
		}
		_, _ = w.Write(buf.Bytes())
	}
	// fail draws an error page in the layout. It keeps the reading when
	// there is one, so the header still says where the stack is.
	fail := func(w http.ResponseWriter, r *http.Request, s Snapshot, code int, msg string) {
		title := http.StatusText(code)
		render(w, r, page{Page: "error", Title: title, S: s, Code: code, Message: msg})
	}
	snap := func(w http.ResponseWriter, r *http.Request) (Snapshot, bool) {
		s, err := readStack(r.Context())
		if err != nil {
			fail(w, r, Snapshot{}, http.StatusInternalServerError, "The stack could not be read: "+err.Error())
			return s, false
		}
		return s, true
	}
	simple := func(name, title string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if s, ok := snap(w, r); ok {
				render(w, r, page{Page: name, Title: title, S: s, Refresh: true})
			}
		}
	}

	mux := http.NewServeMux()
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServerFS(assets)))
	mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/assets/favicons/favicon.svg", http.StatusMovedPermanently)
	})
	mux.HandleFunc("GET /{$}", simple("overview", "Overview"))
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		s, ok := snap(w, r)
		if ok {
			fail(w, r, s, http.StatusNotFound, "Nothing lives at "+r.URL.Path+".")
		}
	})
	mux.HandleFunc("GET /services", simple("services", "Services"))
	mux.HandleFunc("GET /images", simple("images", "Images"))
	mux.HandleFunc("GET /engines", simple("engines", "Engines"))
	mux.HandleFunc("GET /projects", simple("projects", "Projects"))
	mux.HandleFunc("GET /backups", simple("backups", "Backups"))
	mux.HandleFunc("GET /connect", simple("connect", "Connect"))
	mux.HandleFunc("GET /engines/{engine}", func(w http.ResponseWriter, r *http.Request) {
		s, ok := snap(w, r)
		if !ok {
			return
		}
		e := s.Engine(r.PathValue("engine"))
		if e == nil {
			fail(w, r, s, http.StatusNotFound, "There is no engine named "+r.PathValue("engine")+".")
			return
		}
		render(w, r, page{Page: "engine", Title: e.Title, S: s, Engine: e, Refresh: true})
	})
	mux.HandleFunc("GET /projects/{engine}/{name}", func(w http.ResponseWriter, r *http.Request) {
		if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
			http.Error(w, "cross-site request refused", http.StatusForbidden)
			return
		}
		s, ok := snap(w, r)
		if !ok {
			return
		}
		e := s.Engine(r.PathValue("engine"))
		if e == nil {
			fail(w, r, s, http.StatusNotFound, "There is no engine named "+r.PathValue("engine")+".")
			return
		}
		p, err := src.Project(r.Context(), e.Engine, r.PathValue("name"))
		if errors.Is(err, ErrNoProject) {
			fail(w, r, s, http.StatusNotFound, "There is no project named "+r.PathValue("name")+" on "+e.Title+".")
			return
		}
		if err != nil {
			fail(w, r, s, http.StatusInternalServerError, err.Error())
			return
		}
		render(w, r, page{Page: "project", Title: p.Database, S: s, Engine: e, Project: &p})
	})
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		s, ok := snap(w, r)
		if !ok {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(s)
	})
	return secure(mux, allowedHosts), nil
}

func secure(next http.Handler, allowedHosts []string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(allowedHosts) > 0 {
			host := r.Host
			if h, _, err := net.SplitHostPort(host); err == nil {
				host = h
			}
			ok := false
			for _, a := range allowedHosts {
				if strings.EqualFold(host, a) {
					ok = true
				}
			}
			if !ok {
				http.Error(w, "host not allowed", http.StatusMisdirectedRequest)
				return
			}
		}
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self'; style-src-attr 'unsafe-inline'; script-src 'self'; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// envBlock renders a paste-ready block as HTML, with each password value
// wrapped so the page can blur it until the reader asks to see it. The
// text itself is whole, so copying the block copies the password.
//
// The script writes the toolkit CA's path as /path/to/dbpantry/certs/...,
// since it cannot know where the block will be pasted; the dashboard knows
// the checkout, so it shows the real path.
func envBlock(block, root string) template.HTML {
	if root != "" {
		block = strings.ReplaceAll(block, "/path/to/dbpantry", root)
	}
	var b strings.Builder
	for _, line := range strings.Split(strings.TrimRight(block, "\n"), "\n") {
		key, val, ok := strings.Cut(line, "=")
		switch {
		case strings.HasPrefix(line, "#"):
			b.WriteString(`<span class="text-secondary">` + html.EscapeString(line) + `</span>`)
		case ok && strings.HasSuffix(key, "PASSWORD"):
			b.WriteString(html.EscapeString(key) + `=<span class="mdb-secret" data-secret>` + html.EscapeString(val) + `</span>`)
		default:
			b.WriteString(html.EscapeString(line))
		}
		b.WriteString("\n")
	}
	// #nosec G203 -- every piece of input is escaped above
	return template.HTML(b.String())
}

var funcs = template.FuncMap{
	"icon":     icon,
	"envblock": envBlock,
	"bytes": func(n int64) string {
		const unit = 1024
		if n < unit {
			return fmt.Sprintf("%d B", n)
		}
		div, exp := int64(unit), 0
		for m := n / unit; m >= unit; m /= unit {
			div *= unit
			exp++
		}
		return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
	},
	"ago": func(t time.Time) string {
		if t.IsZero() {
			return "–"
		}
		d := time.Since(t)
		switch {
		case d < time.Minute:
			return "just now"
		case d < time.Hour:
			return fmt.Sprintf("%dm ago", int(d.Minutes()))
		case d < 48*time.Hour:
			return fmt.Sprintf("%dh ago", int(d.Hours()))
		default:
			return fmt.Sprintf("%dd ago", int(d.Hours()/24))
		}
	},
	"since": func(t time.Time) string {
		if t.IsZero() {
			return "–"
		}
		d := time.Since(t).Round(time.Minute)
		switch {
		case d < time.Minute:
			return "under a minute"
		case d < time.Hour:
			return fmt.Sprintf("%dm", int(d.Minutes()))
		case d < 48*time.Hour:
			return fmt.Sprintf("%dh %02dm", int(d.Hours()), int(d.Minutes())%60)
		default:
			return fmt.Sprintf("%dd %dh", int(d.Hours()/24), int(d.Hours())%24)
		}
	},
	"clock": func(t time.Time) string { return t.Format("15:04:05") },
	// health maps a health verdict to a Tabler colour.
	"health": func(h string) string {
		switch h {
		case "healthy":
			return "green"
		case "none":
			return "blue"
		case "starting":
			return "yellow"
		default:
			return "red"
		}
	},
	// status is an engine's state for a badge: running, stopped (in the
	// profiles but not up) or off (not in the profiles).
	"status": func(s Snapshot, engine string, enabled bool) string {
		switch {
		case s.Running(engine):
			return "running"
		case enabled:
			return "stopped"
		default:
			return "off"
		}
	},
	"join": strings.Join,
	"base": filepath.Base,
	"path": filepath.Join,
	"upper": func(s string) string {
		return strings.ToUpper(strings.ReplaceAll(s, "-", "_"))
	},
}
