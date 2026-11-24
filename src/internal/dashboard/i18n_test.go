package dashboard

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCatalogsComplete(t *testing.T) {
	if err := CatalogOK(); err != nil {
		t.Fatal(err)
	}
	if got := Translate("es", "nav.help"); got != "Ayuda" {
		t.Fatalf("es nav.help = %q, want Ayuda", got)
	}
	if got := Translate("zz", "nav.help"); got != "Help" {
		t.Fatalf("unknown language falls back to English, got %q", got)
	}
	loadCatalogs()
	en := catalogs[defaultLang]
	for _, l := range SupportedLangs {
		m := catalogs[l.Code]
		for k := range en {
			if _, ok := m[k]; !ok {
				t.Errorf("%s catalog missing key %q (present in en)", l.Code, k)
			}
		}
	}
}

func TestNegotiateLang(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/?lang=fr", nil)
	if got := NegotiateLang(w, r, false); got != "fr" {
		t.Fatalf("?lang=fr → %q", got)
	}
	if c := w.Result().Cookies(); len(c) == 0 || c[0].Name != langCookie || c[0].Value != "fr" {
		t.Fatalf("expected mdb_lang=fr cookie, got %+v", w.Result().Cookies())
	}
	r2 := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	r2.Header.Set("Accept-Language", "de-DE,de;q=0.9,en;q=0.8")
	if got := NegotiateLang(httptest.NewRecorder(), r2, false); got != "de" {
		t.Fatalf("Accept-Language de-DE → %q", got)
	}
}

func TestAboutAndHelpPages(t *testing.T) {
	about := body(t, serve(t, fixture, nil, "/about", ""))
	if strings.Contains(about, "opensource.simtabi.com/products/") {
		t.Error("About must not link the unpublished OSS product path (404 on the portal)")
	}
	for _, want := range []string{
		`lang="en"`, "MIT License", "Tabler", "about.title", // title key must be translated away
		"opensource.simtabi.com/", "github.com/simtabi/dbpantry",
	} {
		if want == "about.title" {
			if strings.Contains(about, ">about.title<") {
				t.Error("About title must be translated, not show the key")
			}
			continue
		}
		if !strings.Contains(about, want) {
			t.Errorf("About lacks %q", want)
		}
	}
	help := body(t, serve(t, fixture, nil, "/help?lang=es", ""))
	if !strings.Contains(help, `lang="es"`) || !strings.Contains(help, "Ayuda") {
		t.Error("Help with ?lang=es must render Spanish chrome")
	}
	if !strings.Contains(help, "Documentación") {
		t.Error("Help Spanish catalog must translate help.docs")
	}
	// In-app Help must not point browsers at GitHub paths that are not on
	// main yet (dashboard.md / api.md were 404 during e2e). Prefer /help,
	// /about and live API routes; keep GH links for docs that exist on main.
	ov := body(t, serve(t, fixture, nil, "/", ""))
	for _, page := range []string{help, ov} {
		if strings.Contains(page, "github.com/simtabi/dbpantry/blob/main/docs/guides/dashboard.md") ||
			strings.Contains(page, "github.com/simtabi/dbpantry/blob/main/docs/reference/api.md") {
			t.Error("Help/nav must not link to unpublished GitHub doc paths that 404 on main")
		}
		if !strings.Contains(page, `href="/help"`) || !strings.Contains(page, `href="/about"`) {
			t.Error("Help and About must be reachable as in-app routes from the chrome")
		}
		if !strings.Contains(page, "/api/v1/actions") {
			t.Error("Management API entry must point at the live /api/v1/actions route")
		}
	}
}
