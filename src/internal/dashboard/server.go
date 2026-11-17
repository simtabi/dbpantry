package dashboard

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"time"
)

//go:embed templates/*.html resources/assets
var files embed.FS

// Collect reads the stack. It is called once per page and per refresh.
type Collect func(context.Context) (Snapshot, error)

// Handler serves the dashboard:
//
//	GET /                 the page
//	GET /partials/body    the page's body alone, which the page fetches to refresh itself
//	GET /api/status       the snapshot as JSON
//	GET /assets/<kind>/…  the stylesheet, script and favicon, embedded
//
// When allowedHosts is set, a request whose Host is not one of them is
// refused, so a web page elsewhere cannot read the dashboard through a DNS
// name that resolves to this machine (DNS rebinding).
func Handler(collect Collect, allowedHosts []string) (http.Handler, error) {
	tpl, err := template.New("").Funcs(funcs).ParseFS(files, "templates/*.html")
	if err != nil {
		return nil, err
	}
	assets, err := fs.Sub(files, "resources/assets")
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServerFS(assets)))
	mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/assets/favicons/favicon.svg", http.StatusMovedPermanently)
	})
	page := func(name string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if name == "page" && r.URL.Path != "/" {
				http.NotFound(w, r)
				return
			}
			snap, err := collect(r.Context())
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			var buf bytes.Buffer
			if err := tpl.ExecuteTemplate(&buf, name, snap); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(buf.Bytes())
		}
	}
	mux.HandleFunc("GET /", page("page"))
	mux.HandleFunc("GET /partials/body", page("body"))
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		snap, err := collect(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(snap)
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

var funcs = template.FuncMap{
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
	"join": strings.Join,
}
