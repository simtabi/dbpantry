package dashboard

import (
	"embed"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
)

//go:embed locales/*.json
var localeFS embed.FS

const (
	defaultLang   = "en"
	langCookie    = "mdb_lang"
	langCookieMax = 365 * 24 * 3600
)

// Lang is one supported UI language.
type Lang struct {
	Code string // BCP 47 primary tag, e.g. "en"
	Name string // endonym shown in the switcher
}

// SupportedLangs are the languages of the dashboard chrome. English is the default and
// the fallback for any missing key in another catalog (design.md D-91).
var SupportedLangs = []Lang{
	{Code: "en", Name: "English"},
	{Code: "es", Name: "Español"},
	{Code: "fr", Name: "Français"},
	{Code: "de", Name: "Deutsch"},
	{Code: "pt", Name: "Português"},
	{Code: "ja", Name: "日本語"},
}

var (
	catalogsOnce sync.Once
	catalogs     map[string]map[string]string
	catalogsErr  error
)

func loadCatalogs() {
	catalogsOnce.Do(func() {
		catalogs = map[string]map[string]string{}
		ents, err := localeFS.ReadDir("locales")
		if err != nil {
			catalogsErr = err
			return
		}
		for _, e := range ents {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			code := strings.TrimSuffix(e.Name(), ".json")
			b, err := localeFS.ReadFile("locales/" + e.Name())
			if err != nil {
				catalogsErr = err
				return
			}
			m := map[string]string{}
			if err := json.Unmarshal(b, &m); err != nil {
				catalogsErr = fmt.Errorf("locales/%s: %w", e.Name(), err)
				return
			}
			catalogs[code] = m
		}
		if catalogs[defaultLang] == nil {
			catalogsErr = fmt.Errorf("locales: missing %s.json", defaultLang)
		}
	})
}

// CatalogOK reports whether every supported language has a catalog.
func CatalogOK() error {
	loadCatalogs()
	if catalogsErr != nil {
		return catalogsErr
	}
	for _, l := range SupportedLangs {
		if catalogs[l.Code] == nil {
			return fmt.Errorf("locales: missing %s.json", l.Code)
		}
	}
	return nil
}

// Translate looks up key in lang, then English, then returns the key.
func Translate(lang, key string) string {
	loadCatalogs()
	if catalogsErr != nil {
		return key
	}
	lang, _ = supportedLang(lang)
	if lang == "" {
		lang = defaultLang
	}
	if m := catalogs[lang]; m != nil {
		if v, ok := m[key]; ok && v != "" {
			return v
		}
	}
	if lang != defaultLang {
		if v, ok := catalogs[defaultLang][key]; ok && v != "" {
			return v
		}
	}
	return key
}

// supportedLang returns the primary tag when it is one we ship.
func supportedLang(code string) (string, bool) {
	code = strings.ToLower(strings.TrimSpace(code))
	if i := strings.IndexAny(code, "-_"); i > 0 {
		code = code[:i]
	}
	for _, l := range SupportedLangs {
		if l.Code == code {
			return code, true
		}
	}
	return "", false
}

// NegotiateLang picks the UI language: ?lang=, then the mdb_lang cookie, then
// Accept-Language, then English. An explicit ?lang= also writes the cookie.
func NegotiateLang(w http.ResponseWriter, r *http.Request, tls bool) string {
	if q := r.URL.Query().Get("lang"); q != "" {
		lang, ok := supportedLang(q)
		if !ok {
			lang = defaultLang
		}
		setLangCookie(w, lang, tls)
		return lang
	}
	if c, err := r.Cookie(langCookie); err == nil && c.Value != "" {
		if lang, ok := supportedLang(c.Value); ok {
			return lang
		}
	}
	return matchAcceptLanguage(r.Header.Get("Accept-Language"))
}

func setLangCookie(w http.ResponseWriter, lang string, tls bool) {
	// A preference, not a credential: the page reads it (not HttpOnly) for
	// the switcher, and Lax lets a link from elsewhere keep the language.
	http.SetCookie(w, &http.Cookie{ // #nosec G124 -- a language preference, readable by the page on purpose
		Name:     langCookie,
		Value:    lang,
		Path:     "/",
		MaxAge:   langCookieMax,
		HttpOnly: false, // readable by the page for the switcher's active state
		SameSite: http.SameSiteLaxMode,
		Secure:   tls,
	})
}

func matchAcceptLanguage(header string) string {
	if header == "" {
		return defaultLang
	}
	best, bestQ := defaultLang, -1.0
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		tag, rest, _ := strings.Cut(part, ";")
		q := 1.0
		if strings.HasPrefix(strings.TrimSpace(rest), "q=") {
			var v float64
			if _, err := fmt.Sscanf(strings.TrimSpace(rest), "q=%f", &v); err == nil {
				q = v
			}
		}
		code, ok := supportedLang(tag)
		if !ok {
			continue
		}
		if q > bestQ {
			best, bestQ = code, q
		}
	}
	return best
}

// LangSwitcherURL builds the current path with ?lang=code (preserving other query).
func LangSwitcherURL(r *http.Request, code string) string {
	q := r.URL.Query()
	q.Set("lang", code)
	u := *r.URL
	u.RawQuery = q.Encode()
	return u.RequestURI()
}
