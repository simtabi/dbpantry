package dashboard

import (
	_ "embed"
	"runtime"
	"strings"
)

//go:embed notices/mit.txt
var mitLicenseText string

//go:embed notices/third-party.md
var thirdPartyNotices string

// EngineLicense is one engine's licence row on the About page.
type EngineLicense struct {
	Name        string
	Title       string
	License     string
	OSIApproved bool
	Note        string
	Publish     string
}

// ComponentNotice is a third-party library shipped in the dashboard binary.
type ComponentNotice struct {
	Name    string
	Version string
	License string
	URL     string
}

// AboutInfo is auto-filled identity for /about: build stamps, toolkit MIT
// text, UI component notices, and per-engine licences from the manifest.
type AboutInfo struct {
	Version string
	Commit  string
	Engines []EngineLicense
}

// aboutView is what the About template renders.
type aboutView struct {
	Version    string
	Commit     string
	GoVersion  string
	Backend    string
	Root       string
	Network    string
	MITText    string
	ThirdParty string
	UI         []ComponentNotice
	Engines    []EngineLicense
}

func buildAbout(o Options, s Snapshot) aboutView {
	a := o.About
	ver := a.Version
	if ver == "" {
		ver = s.Version
	}
	commit := a.Commit
	if commit == "" {
		commit = "none"
	}
	return aboutView{
		Version:    ver,
		Commit:     commit,
		GoVersion:  runtime.Version(),
		Backend:    s.Backend,
		Root:       s.Root,
		Network:    s.Network,
		MITText:    stringsTrimRight(mitLicenseText),
		ThirdParty: stringsTrimRight(thirdPartyNotices),
		UI: []ComponentNotice{
			{Name: "Tabler", Version: "1.7.5", License: "MIT", URL: "https://tabler.io"},
			{Name: "Bootstrap (via Tabler)", Version: "5.x", License: "MIT", URL: "https://getbootstrap.com"},
			{Name: "Tabler Icons (style)", Version: "outline", License: "MIT", URL: "https://tabler.io/icons"},
		},
		Engines: a.Engines,
	}
}

func stringsTrimRight(s string) string {
	return strings.TrimRight(s, " \n\r\t")
}
