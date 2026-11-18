// Package dashboard serves a read-only web page describing the stack: which
// services run and how healthy they are, where each engine listens, the
// projects provisioned on it and the backups taken. It is styled with Tabler
// (https://tabler.io), embedded in the binary, and changes nothing.
package dashboard

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/simtabi/dbpantry/src/internal/endpoint"
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
	// (MDB_<ENGINE>_INIT_EXTENSIONS), which new-project creates.
	Extensions map[string][]string

	Images []Image
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

	BackupDir  string
	Backups    []Backup
	BackupsErr string
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
	Ref     string
	Size    string
	Created string
}

// Project is a database `make new-project` provisioned, found by its
// password files. The files' contents are never read.
type Project struct {
	Engine         string
	Name           string
	Owner          string
	Readonly       string
	OwnerSecret    string
	ReadonlySecret string
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

// HealthPercent is Healthy as a share of the running services.
func (s Snapshot) HealthPercent() int {
	if len(s.Services) == 0 {
		return 0
	}
	return s.Healthy() * 100 / len(s.Services)
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
func (s Snapshot) Running(engine string) bool {
	for _, v := range s.Services {
		if v.Name == engine && (v.State == "" || v.State == "running") && (v.Health == "healthy" || v.Health == "none") {
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
