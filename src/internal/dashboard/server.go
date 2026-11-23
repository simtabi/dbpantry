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
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/simtabi/dbpantry/src/internal/addon"
	"github.com/simtabi/dbpantry/src/internal/endpoint"
	"github.com/simtabi/dbpantry/src/internal/envtype"
	"github.com/simtabi/dbpantry/src/internal/jobs"
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
	// Logs streams a service's log, for the Logs view.
	Logs Logs
}

// ErrNoProject is what Sources.Project returns for a project that does not
// exist, so the page answers 404 rather than 500.
var ErrNoProject = errors.New("no such project")

// pages are the dashboard's pages, each a template of its own over the
// shared layout. The key is the page's name; the menu marks it active.
var pages = []string{"overview", "services", "images", "engines", "engine", "projects", "project", "databases", "backups", "connect", "operations", "config", "jobs", "logs", "status", "system", "addons", "envs", "ha", "help", "about", "error"}

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
	// FilterEngine is an optional ?engine= filter (Add-ons wizard, single).
	FilterEngine string
	// FilterEngines is the comma-joined validated engine list (?engines= or ?engine=).
	FilterEngines string
	// FilterMode is "single" or "multi" for the Add-ons wizard picker.
	FilterMode string
	// Refresh is false on pages that must not replace themselves every 5s:
	// project pages (password reveal) and interactive wizards (Add-ons step
	// / selection). Live still runs elsewhere; a forced post-job refresh can remount.
	Refresh bool
	// Code and Message describe the error page's error.
	Code    int
	Message string
	// Who is looking, for the header, and the CSRF token every change
	// carries (meta csrf-token in the layout).
	Actor string
	Mode  string
	CSRF  string
	// Admin is true for a principal with the admin scope.
	Admin bool
	// Actions are the actions this principal sees, each marked allowed or
	// not with the reason (§3.4: disabled with a reason, never hidden).
	Actions map[string]actionView
	// Jobs and Audit fill the Jobs page.
	Jobs  []jobs.Job
	Audit []jobs.Entry
	// Service is the Logs view's service.
	Service string
	// Running is the change in progress, shown in the header of every page.
	Running *jobs.Job
	// Tokens fill the Operations page's API tokens card.
	Tokens []Token
	// Lang is the negotiated UI language (design.md D-91).
	Lang string
	// LangLinks are switcher entries for the current request URL.
	LangLinks []LangLink
	// About fills the About page.
	About aboutView
}

// LangLink is one entry in the language switcher.
type LangLink struct {
	Code   string
	Name   string
	URL    string
	Active bool
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
	// inflight is non-nil while a reading runs; it is closed when that
	// reading finishes. Cold concurrent callers wait on it instead of
	// starting a second docker inspect storm.
	var inflight chan struct{}

	finish := func(s Snapshot, err error) (Snapshot, error) {
		mu.Lock()
		defer mu.Unlock()
		if err == nil {
			last, at = s, now()
		}
		if inflight != nil {
			close(inflight)
			inflight = nil
		}
		return s, err
	}

	var get func(context.Context) (Snapshot, error)
	get = func(ctx context.Context) (Snapshot, error) {
		mu.Lock()
		if !at.IsZero() {
			s := last
			if now().Sub(at) >= fresh && inflight == nil {
				ch := make(chan struct{})
				inflight = ch
				mu.Unlock()
				bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
				go func() {
					defer cancel()
					s2, err := read(bg)
					_, _ = finish(s2, err)
				}()
				return s, nil
			}
			mu.Unlock()
			return s, nil
		}
		if inflight != nil {
			ch := inflight
			mu.Unlock()
			select {
			case <-ch:
				return get(ctx)
			case <-ctx.Done():
				return Snapshot{}, ctx.Err()
			}
		}
		inflight = make(chan struct{})
		mu.Unlock()
		s, err := read(ctx)
		return finish(s, err)
	}
	return get
}

// Handler serves the dashboard:
//
//	GET /                          overview
//	GET /services, /images, /engines, /projects, /backups, /connect, /jobs
//	GET /engines/{engine}          one engine
//	GET /projects/{engine}/{name}  one project, with its .env block
//	GET /services/{service}/logs   one service's log, streamed
//	GET /api/status                the snapshot as JSON (no passwords); also /api/v1/status
//	GET /api/v1/actions            every action, and whether this principal may run it
//	POST /api/v1/jobs              start an action: {action, params, confirm}
//	GET /api/v1/jobs               jobs in memory and the audit log
//	GET /api/v1/jobs/{id}          one job, with its output
//	GET /api/v1/jobs/{id}/events   the job as server-sent events
//	GET /api/v1/services/{service}/logs  a service's log as server-sent events
//	GET, POST /login; POST /logout password sign-in, sign-out
//	GET /assets/<kind>/…           stylesheets, scripts, favicon, embedded
//
// Every route but the assets and the login form goes through the auth gate
// (gate), and every change through its CSRF and same-origin checks. The API
// is the contract: the pages are its clients and can do nothing it cannot.
//
// A request with "X-Partial: 1" gets the page's content alone, which the
// page fetches every five seconds to refresh itself.
//
// When allowedHosts is set, a request whose Host is not one of them is
// refused, so a web page elsewhere cannot read the dashboard through a DNS
// name that resolves to this machine (DNS rebinding). A project page, which
// carries a password, is also refused to a cross-site request.
func Handler(src Sources, o Options) (http.Handler, error) {
	if o.Auth == nil || o.Runner == nil {
		return nil, errors.New("dashboard: Options.Auth and Options.Runner are required")
	}
	if err := CatalogOK(); err != nil {
		return nil, fmt.Errorf("dashboard locales: %w", err)
	}
	if o.Env == nil {
		o.Env = func(string) string { return "" }
	}
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
	if o.Choices == nil {
		o.Choices = func(ctx context.Context) (Choices, error) {
			s, err := readStack(ctx)
			if err != nil {
				return Choices{}, err
			}
			c := ChoicesOf(s)
			if o.Volumes != nil {
				if c.Volumes, err = o.Volumes(ctx); err != nil {
					return Choices{}, err
				}
			}
			return c, nil
		}
	}

	render := func(w http.ResponseWriter, r *http.Request, p page) {
		who := principal(r)
		p.Actor, p.Mode, p.CSRF, p.Admin = who.Actor, o.Auth.Mode(), who.CSRF, who.Scope >= ScopeAdmin
		p.Actions = map[string]actionView{}
		for _, v := range views(o, who) {
			p.Actions[v.Name] = v
		}
		if j, ok := o.Runner.Running(); ok {
			p.Running = &j
		}
		// Recent jobs feed the header notifications dropdown (Tabler pattern).
		if p.Jobs == nil {
			p.Jobs = o.Runner.List()
		}
		p.Lang = NegotiateLang(w, r, o.TLS)
		p.LangLinks = make([]LangLink, 0, len(SupportedLangs))
		for _, l := range SupportedLangs {
			p.LangLinks = append(p.LangLinks, LangLink{
				Code: l.Code, Name: l.Name, URL: LangSwitcherURL(r, l.Code), Active: l.Code == p.Lang,
			})
		}
		switch p.Page {
		case "help":
			p.Title = Translate(p.Lang, "help.title")
		case "status":
			p.Title = Translate(p.Lang, "status.title")
		case "about":
			p.Title = Translate(p.Lang, "about.title")
			p.About = buildAbout(o, p.S)
		}
		var buf bytes.Buffer
		name := "layout"
		if r.Header.Get("X-Partial") == "1" {
			name = "partial"
		}
		if err := sets[p.Page].ExecuteTemplate(&buf, name, p); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Language", p.Lang)
		w.Header().Add("Vary", "Cookie")
		w.Header().Add("Vary", "Accept-Language")
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
	mux.HandleFunc("GET /config", simple("config", "Configuration"))
	mux.HandleFunc("GET /status", simple("status", "Status"))
	mux.HandleFunc("GET /system", simple("system", "System"))
	mux.HandleFunc("GET /addons", func(w http.ResponseWriter, r *http.Request) {
		s, ok := snap(w, r)
		if !ok {
			return
		}
		eng, engines, mode := parseAddonEngineFilter(r)
		render(w, r, page{
			// No Live tick: replacing the wizard DOM dropped step/selection
			// state and (with pathname-only fetch) lost ?engine= — felt stuck loading.
			Page: "addons", Title: "Add-ons", S: s, Refresh: false,
			FilterEngine: eng, FilterEngines: engines, FilterMode: mode,
		})
	})
	mux.HandleFunc("GET /databases", func(w http.ResponseWriter, r *http.Request) {
		s, ok := snap(w, r)
		if !ok {
			return
		}
		// No Live tick: the provision wizard keeps step/form state in the DOM.
		render(w, r, page{Page: "databases", Title: "Databases", S: s, Refresh: false})
	})
	mux.HandleFunc("GET /envs", simple("envs", "Environments"))
	mux.HandleFunc("GET /ha", simple("ha", "Primary & replicas"))
	mux.HandleFunc("GET /help", simple("help", "Help"))
	mux.HandleFunc("GET /about", simple("about", "About"))
	mux.HandleFunc("GET /operations", func(w http.ResponseWriter, r *http.Request) {
		s, ok := snap(w, r)
		if !ok {
			return
		}
		var tokens []Token
		if o.Auth.tokens != nil {
			var err error
			if tokens, err = o.Auth.tokens.List(); err != nil {
				fail(w, r, s, http.StatusInternalServerError, "The API tokens could not be read: "+err.Error())
				return
			}
		}
		render(w, r, page{Page: "operations", Title: "Operations", S: s, Tokens: tokens, Refresh: true})
	})
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
	status := func(w http.ResponseWriter, r *http.Request) {
		s, ok := snap(w, r)
		if !ok {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(s)
	}
	mux.HandleFunc("GET /api/status", status)
	mux.HandleFunc("GET /api/v1/status", status)
	mux.HandleFunc("GET /jobs", func(w http.ResponseWriter, r *http.Request) {
		s, ok := snap(w, r)
		if !ok {
			return
		}
		audit, err := jobs.ReadAudit(o.Audit, 100)
		if err != nil {
			fail(w, r, s, http.StatusInternalServerError, "The audit log could not be read: "+err.Error())
			return
		}
		render(w, r, page{Page: "jobs", Title: "Jobs", S: s, Jobs: o.Runner.List(), Audit: audit, Refresh: true})
	})
	mux.HandleFunc("GET /services/{service}/logs", func(w http.ResponseWriter, r *http.Request) {
		s, ok := snap(w, r)
		if !ok {
			return
		}
		svc := r.PathValue("service")
		if !contains(ChoicesOf(s).Services, svc) {
			fail(w, r, s, http.StatusNotFound, "There is no service named "+svc+" in the stack.")
			return
		}
		render(w, r, page{Page: "logs", Title: svc + " logs", S: s, Service: svc})
	})
	mux.HandleFunc("GET /api/v1/actions", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, views(o, principal(r)))
	})
	mux.HandleFunc("GET /api/v1/choices", func(w http.ResponseWriter, r *http.Request) {
		c, err := o.Choices(r.Context())
		if err != nil {
			apiError(w, http.StatusServiceUnavailable, "the stack could not be read: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, c)
	})
	mux.HandleFunc("POST /api/v1/jobs", startJob(o))
	mux.HandleFunc("GET /api/v1/jobs", history(o))
	mux.HandleFunc("GET /api/v1/jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		j, err := o.Runner.Get(r.PathValue("id"))
		if err != nil {
			apiError(w, http.StatusNotFound, err.Error())
			return
		}
		for i, l := range j.Lines {
			j.Lines[i] = sanitizeLine(l)
		}
		writeJSON(w, http.StatusOK, j)
	})
	mux.HandleFunc("GET /api/v1/jobs/{id}/events", events(o))
	mux.HandleFunc("POST /api/v1/jobs/{id}/control", controlJob(o))
	tokenRoutes(mux, o)
	hookRoutes(mux, o)
	mux.HandleFunc("GET /api/v1/services/{service}/logs", logStream(o, src.Logs))
	mux.HandleFunc("GET /login", login(o))
	mux.HandleFunc("POST /login", login(o))
	mux.HandleFunc("POST /logout", logout(o))
	return secure(gate(mux, o), o.AllowedHosts), nil
}

// ChoicesOf is what a snapshot offers an action's parameters: its engines
// (running ones first), the services and fixture services that exist, and its dumps. Volumes come
// from the backend (the command that serves the dashboard sets Choices).
func ChoicesOf(s Snapshot) Choices {
	var c Choices
	var stopped []string
	seen := map[string]bool{}
	addProfile := func(p string) {
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		c.Profiles = append(c.Profiles, p)
	}
	for _, e := range s.Endpoints.Endpoints {
		if s.Running(e.Engine) {
			c.Running = append(c.Running, e.Engine)
		} else {
			stopped = append(stopped, e.Engine)
		}
		addProfile(e.Engine)
		if e.Pooled != nil {
			addProfile(e.Engine + "-pooler")
		}
	}
	c.Engines = append(append(c.Engines, c.Running...), stopped...)
	for _, svc := range s.Services {
		c.Services = append(c.Services, svc.Name)
	}
	for _, b := range s.Backups {
		c.Dumps = append(c.Dumps, b.File)
	}
	for _, p := range s.Endpoints.Profiles {
		addProfile(p)
		c.ActiveProfiles = append(c.ActiveProfiles, p)
	}
	// Stack extras that Compose declares beyond per-engine profiles.
	for _, p := range []string{"ui", "ha", "pooler", "backup", "pitr", "fixture"} {
		addProfile(p)
	}
	slices.Sort(c.Profiles)
	c.EnvTypes = envtype.IDs()
	if names, err := envtype.Names(envtype.RegistryPath(envtype.OperatorHome())); err == nil {
		c.EnvNames = names
	}
	return c
}

// loginTemplate is the password-mode sign-in page, outside the layout: it is
// shown before there is a session to read the stack with.
var loginTemplate = template.Must(template.ParseFS(files, "templates/login.html"))

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
	"icon":         icon,
	"envblock":     envBlock,
	"actionButton": actionButton,
	"actionsIn":    actionsIn,
	"areaCard":     areaCard,
	"engineMenu":   engineMenu,
	"projectMenu":  projectMenu,
	"serviceMenu":  serviceMenu,
	"profilesWith": profilesWith,
	"hasProfile": func(profiles []string, name string) bool {
		return slices.Contains(profiles, name)
	},
	"statusAddons": func(rows []addon.Status) []addon.Status { return addon.ForStatusPage(rows) },
	"addonsByEngine": func(s Snapshot) []addon.EngineGroup {
		titleOf := func(eng string) string {
			for _, e := range s.Endpoints.Endpoints {
				if e.Engine == eng {
					return e.Title
				}
			}
			return eng
		}
		return addon.GroupByEngine(addon.ForStatusPage(s.Addons), titleOf)
	},
	"addonsCatalogGrouped": func(s Snapshot) []addon.EngineGroup {
		titleOf := func(eng string) string {
			for _, e := range s.Endpoints.Endpoints {
				if e.Engine == eng {
					return e.Title
				}
			}
			return eng
		}
		return addon.GroupByEngine(s.Addons, titleOf)
	},
	"addonsForGrouped": func(rows []addon.Status, eng, title string) []addon.EngineGroup {
		var out []addon.Status
		for _, r := range rows {
			if r.Engine == eng && r.Supported {
				out = append(out, r)
			}
		}
		return addon.GroupByEngine(out, func(string) string { return title })
	},
	"addonsFor": func(rows []addon.Status, eng string) []addon.Status {
		// Engine strip: supported only (design.md D-96).
		var out []addon.Status
		for _, r := range rows {
			if r.Engine == eng && r.Supported {
				out = append(out, r)
			}
		}
		return out
	},
	"kindLabel": addon.KindLabel,
	"dict": func(values ...any) (map[string]any, error) {
		if len(values)%2 != 0 {
			return nil, fmt.Errorf("dict: need even number of args")
		}
		m := make(map[string]any, len(values)/2)
		for i := 0; i < len(values); i += 2 {
			k, ok := values[i].(string)
			if !ok {
				return nil, fmt.Errorf("dict: keys must be strings")
			}
			m[k] = values[i+1]
		}
		return m, nil
	},
	"toJSON": func(v any) template.JS {
		b, err := json.Marshal(v)
		if err != nil {
			return "null"
		}
		// #nosec G203 -- JSON is safe inside a script type=application/json or JS literal
		return template.JS(b)
	},
	"commandLine": commandLine,
	"t": func(p page, key string) string {
		return Translate(p.Lang, key)
	},
	"actionTitle": func(name string) string {
		if a, ok := actionByName(name); ok {
			return a.Title
		}
		return name
	},
	"ms": ms,
	"percent": func(n, d int) int {
		if d <= 0 {
			return 0
		}
		return n * 100 / d
	},
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
	"imageGroups": ImageGroups,
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
	// status is an engine's state for a badge: running, orphan (container
	// still up after profiles dropped it), stopped (in the profiles but
	// not up) or off (not in the profiles and no container).
	"status": func(s Snapshot, engine string, enabled bool) string {
		up := s.Running(engine)
		switch {
		case up && enabled:
			return "running"
		case up && !enabled:
			return "orphan"
		case enabled:
			return "stopped"
		default:
			return "off"
		}
	},
	"stateOf":    stateOf,
	"stateGuide": func() []StateGroup { return StateGuide },
	"stateGroup": stateGroup,
	"join":       strings.Join,
	"base":       filepath.Base,
	"path":       filepath.Join,
	"upper": func(s string) string {
		return strings.ToUpper(strings.ReplaceAll(s, "-", "_"))
	},
	"csvHas":  csvHas,
	"missing": missing,
}

// parseAddonEngineFilter reads ?engine= / ?engines= for the Add-ons wizard.
// engines= wins and forces multi mode; a single engine= uses single mode.
func parseAddonEngineFilter(r *http.Request) (one, csv, mode string) {
	re := patterns[KindEngine]
	valid := func(s string) bool {
		s = strings.TrimSpace(s)
		return s != "" && re != nil && re.MatchString(s)
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("engines")); raw != "" {
		seen := map[string]bool{}
		var out []string
		for _, p := range strings.Split(raw, ",") {
			p = strings.TrimSpace(p)
			if !valid(p) || seen[p] {
				continue
			}
			seen[p] = true
			out = append(out, p)
		}
		if len(out) == 0 {
			return "", "", "multi"
		}
		return out[0], strings.Join(out, ","), "multi"
	}
	eng := strings.TrimSpace(r.URL.Query().Get("engine"))
	if !valid(eng) {
		return "", "", "multi"
	}
	return eng, eng, "single"
}

// csvHas reports whether item appears in a comma-separated list.
func csvHas(csv, item string) bool {
	if csv == "" || item == "" {
		return false
	}
	for _, p := range strings.Split(csv, ",") {
		if strings.TrimSpace(p) == item {
			return true
		}
	}
	return false
}

// missing are the requested values that active lacks; none when active is
// nil, which is not knowing rather than having none.
func missing(requested, active []string) []string {
	if active == nil {
		return nil
	}
	var out []string
	for _, r := range requested {
		if !slices.Contains(active, r) {
			out = append(out, r)
		}
	}
	return out
}
