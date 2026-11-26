// Package dashboard serves the web dashboard: pages describing the stack
// (which services run and how healthy they are, where each engine listens,
// the projects provisioned on it, the backups taken) and the management API
// the pages use to change it. Every change is one dbpantry command run as a
// job (actions.go), so the dashboard can do nothing the command line cannot.
// It is styled with Tabler (https://tabler.io), embedded in the binary.
package dashboard

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/simtabi/dbpantry/src/internal/addon"
	"github.com/simtabi/dbpantry/src/internal/doctor"
	"github.com/simtabi/dbpantry/src/internal/endpoint"
	"github.com/simtabi/dbpantry/src/internal/project"
)

// Snapshot is everything one page shows, read at one moment.
type Snapshot struct {
	Generated time.Time
	Version   string
	Backend   string
	// Root is the checkout, so the page can name the CA's full path.
	Root string
	// Network is the stack's network, which application containers join.
	Network   string
	Endpoints endpoint.Document
	// Extensions are the extensions requested per engine
	// (MDB_<ENGINE>_INIT_EXTENSIONS), which new-project tries to create;
	// each Project's Extensions say which it has.
	Extensions map[string][]string

	Images []Image
	// ImagePrefix is the current MDB_IMAGE_PREFIX (D-78); ImageGroups puts
	// its images first so an old prefix's builds stay visible (audit B7).
	ImagePrefix string
	// ImagesErr says why images could not be listed.
	ImagesErr string

	Services []Service
	// ServicesErr says why services could not be read: the runtime is
	// unreachable, which is not the same as nothing running.
	ServicesErr string

	// FixtureProject is the fixture's compose project (`fixture up`, the
	// disposable engines test suites use), and Fixture its containers.
	FixtureProject string
	Fixture        []Service

	Projects []Project
	// Unmanaged are the databases the engines have that no password file
	// names: dbpantry did not provision them and will not drop them.
	Unmanaged []Project
	// ProjectsErr says why the engines could not be asked about the
	// projects, which are then listed by their password files alone.
	ProjectsErr string

	BackupDir  string
	Backups    []Backup
	BackupsErr string

	// PITR is each PITR-capable engine's on/off state (audit B8).
	PITR []PITRStatus
	// LastVerify is the most recent backup.verify job from the audit log.
	LastVerify *VerifyResult

	// Config is the effective settings for the Configuration page
	// (config show --origin).
	Config []ConfigEntry
	// ConfigFile is the env file those settings were read from.
	ConfigFile string

	// WebUIs are the browser front ends the ui profile runs behind Caddy
	// (Adminer, phpMyAdmin, pgAdmin), when it is enabled.
	WebUIs []WebUI

	// Addons are poolers, extensions, embed toggles and UIs with effective
	// state (design.md D-96). Status shows ForStatusPage only.
	Addons []addon.Status
	// Doctor is the host/runtime report for /system (design.md D-93).
	Doctor *doctor.Report

	// Environments are named instance homes from the operator registry
	// (~/.config/dbpantry/envs.json, design.md D-100).
	Environments []Environment
}

// Environment is one registered MDB_HOME labelled by environment type.
type Environment struct {
	Name    string
	Type    string
	Home    string
	Project string
	Notes   string
}

// ConfigEntry is one setting on the Configuration page.
type ConfigEntry struct {
	Key     string
	Value   string
	Origin  string
	Group   string
	Comment string
	Secret  bool
}

// PITRStatus is one engine's point-in-time recovery posture.
type PITRStatus struct {
	Engine  string
	Capable bool
	Enabled bool
	Summary string
}

// VerifyResult is the last backup.verify run recorded in the audit log.
type VerifyResult struct {
	At     time.Time
	Result string
	Actor  string
}

// WebUI is one browser front end and where it answers.
type WebUI struct {
	Name string
	URL  string
}

// Service is one service's container: its state (running, exited, ...),
// its health check's verdict (healthy, unhealthy, starting, or none when it
// has none), its image and when it started. State, Image and Started are
// empty on a backend that reports health only.
type Service struct {
	Name    string
	Health  string
	State   string
	Image   string
	Started time.Time
}

// Image is one of the stack's locally built images.
type Image struct {
	Ref         string
	Size        string
	Created     string
	Prefix      string
	Unsupported bool
}

// ImageGroup is the images built under one registry prefix.
type ImageGroup struct {
	Prefix string
	Images []Image
}

// ParseImage turns one images listing row into an Image. support is the
// io.simtabi.dbpantry.support label ("unsupported" for an MDB_*_BASE_IMAGE
// build, design.md D-57).
func ParseImage(ref, size, created, support string) Image {
	return Image{
		Ref: ref, Size: size, Created: created, Prefix: imagePrefixOf(ref),
		Unsupported: support == "unsupported",
	}
}

// imagePrefixOf is the registry path before /dbpantry-, or empty.
func imagePrefixOf(ref string) string {
	i := strings.Index(ref, "/dbpantry-")
	if i <= 0 {
		return ""
	}
	return ref[:i]
}

// ImageGroups groups images by prefix, with the current prefix first and
// the others alphabetical. An empty current leaves every group sorted.
func ImageGroups(images []Image, current string) []ImageGroup {
	by := map[string][]Image{}
	var order []string
	for _, im := range images {
		p := im.Prefix
		if p == "" {
			p = imagePrefixOf(im.Ref)
			im.Prefix = p
		}
		if _, ok := by[p]; !ok {
			order = append(order, p)
		}
		by[p] = append(by[p], im)
	}
	sort.Strings(order)
	if current != "" {
		rest := order[:0:0]
		for _, p := range order {
			if p != current {
				rest = append(rest, p)
			}
		}
		if _, ok := by[current]; ok {
			order = append([]string{current}, rest...)
		} else {
			order = rest
		}
	}
	out := make([]ImageGroup, 0, len(order))
	for _, p := range order {
		out = append(out, ImageGroup{Prefix: p, Images: by[p]})
	}
	return out
}

// Project is a database `make new-project` provisioned, found by its
// password files. The files' contents are never read. State and Extensions
// are what its engine says (Reconcile); State is empty when the engines
// could not be asked, and Extensions nil when they are not known.
type Project struct {
	Engine         string
	Name           string
	Owner          string
	Readonly       string
	OwnerSecret    string
	ReadonlySecret string
	State          project.State
	Extensions     []string
}

// Reconcile sets each project's State and Extensions from what the engines
// say, and returns the databases no password file names (project.Unmanaged).
func Reconcile(ps []Project, l project.Listing) []Project {
	known := make([]project.Known, len(ps))
	for i, p := range ps {
		known[i] = project.Known{Engine: p.Engine, Name: p.Name}
	}
	entries := l.Join(known)
	for i := range ps {
		ps[i].State, ps[i].Extensions = entries[i].State, entries[i].Extensions
	}
	var unmanaged []Project
	for _, e := range entries[len(ps):] {
		unmanaged = append(unmanaged, Project{Engine: e.Engine, Name: e.Name, State: e.State, Extensions: e.Extensions})
	}
	return unmanaged
}

// Backup is one dump in the backup directory.
type Backup struct {
	File     string
	Engine   string
	Size     int64
	Modified time.Time
}

// Healthy counts the running services that are healthy or have no check
// to fail.
func (s Snapshot) Healthy() int {
	n := 0
	for _, v := range s.Services {
		if (v.State == "" || v.State == "running") && (v.Health == "healthy" || v.Health == "none") {
			n++
		}
	}
	return n
}

// ServicesUp counts containers that are running (including starting).
func (s Snapshot) ServicesUp() int {
	n := 0
	for _, v := range s.Services {
		if v.State == "" || v.State == "running" || v.State == "starting" {
			n++
		}
	}
	return n
}

// ServicesDown is services that exist but are not up.
func (s Snapshot) ServicesDown() int {
	n := len(s.Services) - s.ServicesUp()
	if n < 0 {
		return 0
	}
	return n
}

// EnginesLeftover counts engines whose container is still up after the
// profile dropped them (nav badge "leftover", formerly labelled "up · off").
func (s Snapshot) EnginesLeftover() int {
	n := 0
	for _, e := range s.Endpoints.Endpoints {
		if !e.Enabled && s.Running(e.Engine) {
			n++
		}
	}
	return n
}

// EnginesRunning counts enabled engines whose service is up.
func (s Snapshot) EnginesRunning() int {
	n := 0
	for _, e := range s.Endpoints.Endpoints {
		if e.Enabled && s.Running(e.Engine) {
			n++
		}
	}
	return n
}

// HealthPercent is Healthy as a share of the running services.
func (s Snapshot) HealthPercent() int {
	if len(s.Services) == 0 {
		return 0
	}
	return s.Healthy() * 100 / len(s.Services)
}

// EnginesPercent is EnginesRunning as a share of Enabled (0 when none enabled).
func (s Snapshot) EnginesPercent() int {
	if s.Enabled() == 0 {
		return 0
	}
	return s.EnginesRunning() * 100 / s.Enabled()
}

// ActiveProjects counts password-file projects whose database still exists.
func (s Snapshot) ActiveProjects() int {
	n := 0
	for _, p := range s.Projects {
		if p.State == project.Active {
			n++
		}
	}
	return n
}

// Enabled counts the engines in the selected profiles.
func (s Snapshot) Enabled() int {
	n := 0
	for _, e := range s.Endpoints.Endpoints {
		if e.Enabled {
			n++
		}
	}
	return n
}

// Setting is one key of the instance configuration; a key the config list
// does not carry comes back with only its Key set.
func (s Snapshot) Setting(key string) ConfigEntry {
	for _, c := range s.Config {
		if c.Key == key {
			return c
		}
	}
	return ConfigEntry{Key: key}
}

// ConfigFromFile counts the keys the instance .env sets.
func (s Snapshot) ConfigFromFile() int {
	n := 0
	for _, c := range s.Config {
		if c.Origin == "file" {
			n++
		}
	}
	return n
}

// ProfileExtras is the selected profiles that are not engines: stack
// extras such as the UIs, HA or the poolers.
func (s Snapshot) ProfileExtras() []string {
	engines := map[string]bool{}
	for _, e := range s.Endpoints.Endpoints {
		engines[e.Engine] = true
	}
	var out []string
	for _, p := range s.Endpoints.Profiles {
		if !engines[p] {
			out = append(out, p)
		}
	}
	return out
}

// IsEngine reports whether a profile names an engine.
func (s Snapshot) IsEngine(profile string) bool {
	for _, e := range s.Endpoints.Endpoints {
		if e.Engine == profile {
			return true
		}
	}
	return false
}

// BackupBytes is the size of every backup together.
func (s Snapshot) BackupBytes() int64 {
	var n int64
	for _, b := range s.Backups {
		n += b.Size
	}
	return n
}

// Running reports whether an engine's service is up: running, and healthy
// or without a health check. An engine can be enabled (in the profiles)
// and still not be running, when the stack is down.
func (s Snapshot) Running(engine string) bool { return s.Up(engine) }

// Up reports whether a service (an engine, its pooler, a front end) is
// running, and healthy or without a health check.
func (s Snapshot) Up(service string) bool {
	for _, v := range s.Services {
		if v.Name == service && (v.State == "" || v.State == "running") && (v.Health == "healthy" || v.Health == "none") {
			return true
		}
	}
	return false
}

// Engine is the endpoint of the named engine, or nil.
func (s Snapshot) Engine(name string) *endpoint.Endpoint {
	for i := range s.Endpoints.Endpoints {
		if s.Endpoints.Endpoints[i].Engine == name {
			return &s.Endpoints.Endpoints[i]
		}
	}
	return nil
}

// Orphaned are the projects whose engine answers and has no such database.
func (s Snapshot) Orphaned() []Project {
	var out []Project
	for _, p := range s.Projects {
		if p.State == project.Orphaned {
			out = append(out, p)
		}
	}
	return out
}

// ProjectsOn are the projects provisioned on one engine.
func (s Snapshot) ProjectsOn(engine string) []Project {
	var out []Project
	for _, p := range s.Projects {
		if p.Engine == engine {
			out = append(out, p)
		}
	}
	return out
}

// SortedProjects lists password-file projects with active first, then
// unknown, then orphaned — so the nav badge's list and the overview table
// put the ones you can open at the top.
func (s Snapshot) SortedProjects() []Project {
	var active, unknown, orphaned []Project
	for _, p := range s.Projects {
		switch p.State {
		case project.Orphaned:
			orphaned = append(orphaned, p)
		case project.Active:
			active = append(active, p)
		default:
			unknown = append(unknown, p)
		}
	}
	out := make([]Project, 0, len(s.Projects))
	out = append(out, active...)
	out = append(out, unknown...)
	out = append(out, orphaned...)
	return out
}

// EndpointsEnabledFirst lists engines running first, then enabled-but-down,
// then the rest — so Connect, overview and the Stack nav do not bury a live
// engine under "not in profiles" rows (containers can still be up after
// MDB_PROFILES was narrowed).
func (s Snapshot) EndpointsEnabledFirst() []endpoint.Endpoint {
	var running, enabled, off []endpoint.Endpoint
	for _, e := range s.Endpoints.Endpoints {
		switch {
		case s.Running(e.Engine):
			running = append(running, e)
		case e.Enabled:
			enabled = append(enabled, e)
		default:
			off = append(off, e)
		}
	}
	out := make([]endpoint.Endpoint, 0, len(s.Endpoints.Endpoints))
	out = append(out, running...)
	out = append(out, enabled...)
	out = append(out, off...)
	return out
}

// BackupsOf are the dumps of one engine.
func (s Snapshot) BackupsOf(engine string) []Backup {
	var out []Backup
	for _, b := range s.Backups {
		if b.Engine == engine {
			out = append(out, b)
		}
	}
	return out
}

// Latest is the newest backup, or nil.
func (s Snapshot) Latest() *Backup {
	if len(s.Backups) == 0 {
		return nil
	}
	return &s.Backups[0]
}

const (
	ownerSuffix    = "_user_password.txt"
	readonlySuffix = "_user_readonly_password.txt"
)

// ScanProjects finds projects by the password files new-project writes,
// secrets/<engine>_<project>_user_password.txt. engines are the engine
// names, so a project whose name holds an underscore is read correctly.
func ScanProjects(secretsDir string, engines []string) ([]Project, error) {
	entries, err := os.ReadDir(secretsDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Project
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ownerSuffix) || strings.HasSuffix(name, readonlySuffix) {
			continue
		}
		stem := strings.TrimSuffix(name, ownerSuffix)
		for _, eng := range engines {
			project, ok := strings.CutPrefix(stem, eng+"_")
			if !ok || project == "" {
				continue
			}
			p := Project{
				Engine: eng, Name: project,
				Owner: project + "_user", Readonly: project + "_user_readonly",
				OwnerSecret: filepath.Join("secrets", name),
			}
			if _, err := os.Stat(filepath.Join(secretsDir, eng+"_"+project+readonlySuffix)); err == nil {
				p.ReadonlySecret = filepath.Join("secrets", eng+"_"+project+readonlySuffix)
			}
			out = append(out, p)
			break
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Engine != out[j].Engine {
			return out[i].Engine < out[j].Engine
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// ScanBackups lists the dumps in dir, newest first. A dump is named
// <engine>_<db>_<timestamp>.<ext>; files that do not start with an engine
// name are not dumps and are left out.
func ScanBackups(dir string, engines []string) ([]Backup, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Backup
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		eng := ""
		for _, name := range engines {
			if strings.HasPrefix(e.Name(), name+"_") {
				eng = name
				break
			}
		}
		if eng == "" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, Backup{File: e.Name(), Engine: eng, Size: info.Size(), Modified: info.ModTime()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Modified.After(out[j].Modified) })
	return out, nil
}
