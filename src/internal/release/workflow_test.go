// Package release_test guards how dbpantry publishes. It holds tests only: the
// release workflow is YAML, so there is no Go code here to test, just a policy
// to pin (docs/guides/release.md, design.md D-76).
package release_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// repoRoot walks up from the test's directory to the repository root: the
// directory holding src/go.mod. The module lives in src/, so the root is its
// parent, not the directory holding go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "src", "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("src/go.mod not found above the test directory")
		}
		dir = parent
	}
}

// workflow is the part of a GitHub Actions workflow this policy reads. Jobs
// stay a node so their order, which decides the first job, survives decoding.
type workflow struct {
	On   map[string]yaml.Node `yaml:"on"`
	Jobs yaml.Node            `yaml:"jobs"`
}

type job struct {
	Needs yaml.Node `yaml:"needs"`
	Steps []step    `yaml:"steps"`
}

type step struct {
	Uses string `yaml:"uses"`
	Run  string `yaml:"run"`
}

type dispatch struct {
	Inputs map[string]struct {
		Required bool   `yaml:"required"`
		Type     string `yaml:"type"`
	} `yaml:"inputs"`
}

// releaseProblems returns every way a release workflow breaks the rule: it may
// start only from a workflow_dispatch with a required confirm input, its first
// job's first step must refuse unless confirm is release-go on a vX.Y.Z tag,
// and every other job must wait for that job, so a refusal publishes nothing.
func releaseProblems(t *testing.T, src []byte) []string {
	t.Helper()
	var wf workflow
	if err := yaml.Unmarshal(src, &wf); err != nil {
		t.Fatalf("release.yml does not parse: %v", err)
	}
	var problems []string

	for trigger := range wf.On {
		if trigger != "workflow_dispatch" {
			problems = append(problems, "trigger "+trigger+": a release runs on workflow_dispatch only, never by itself")
		}
	}
	node, ok := wf.On["workflow_dispatch"]
	if !ok {
		problems = append(problems, "no workflow_dispatch trigger")
	} else {
		var d dispatch
		if err := node.Decode(&d); err != nil {
			t.Fatalf("workflow_dispatch does not decode: %v", err)
		}
		if in, ok := d.Inputs["confirm"]; !ok || !in.Required {
			problems = append(problems, "workflow_dispatch lacks a required confirm input")
		}
	}

	if wf.Jobs.Kind != yaml.MappingNode || len(wf.Jobs.Content) < 2 {
		return append(problems, "release.yml has no jobs")
	}
	jobs := wf.Jobs.Content // key, value, key, value, ...
	first := jobs[0].Value
	var gate job
	if err := jobs[1].Decode(&gate); err != nil {
		t.Fatalf("job %s does not decode: %v", first, err)
	}
	if len(gate.Steps) == 0 {
		problems = append(problems, "job "+first+" has no steps")
	} else {
		s := gate.Steps[0]
		if s.Uses != "" {
			problems = append(problems, "the first step of "+first+" is "+s.Uses+"; the refusal must come before any checkout or build")
		}
		for _, want := range []string{`"release-go"`, "refs/tags/v[0-9]*", "exit 1"} {
			if !strings.Contains(s.Run, want) {
				problems = append(problems, "the first step of "+first+" must refuse without confirm=release-go on a vX.Y.Z tag; it lacks "+want)
			}
		}
	}

	for i := 2; i+1 < len(jobs); i += 2 {
		name := jobs[i].Value
		var j job
		if err := jobs[i+1].Decode(&j); err != nil {
			t.Fatalf("job %s does not decode: %v", name, err)
		}
		var needs []string
		switch j.Needs.Kind {
		case yaml.ScalarNode:
			needs = []string{j.Needs.Value}
		case yaml.SequenceNode:
			if err := j.Needs.Decode(&needs); err != nil {
				t.Fatalf("job %s needs does not decode: %v", name, err)
			}
		}
		if !slices.Contains(needs, first) {
			problems = append(problems, "job "+name+" does not need "+first+", so it would run past the refusal")
		}
	}
	return problems
}

// Pushing a tag publishes nothing. release.yml publishes dbpantry's images and
// GitHub release only when dispatched on a vX.Y.Z tag with
// -f confirm=release-go, and its first step refuses anything else. A tag push
// that started a release nobody wanted yet is how this rule began (2026-11-02,
// in another repository).
func TestReleaseRunsOnlyOnAConfirmedDispatch(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "release.yml")) // #nosec G304 -- a fixed file of this repository
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range releaseProblems(t, src) {
		t.Error("release.yml: " + p)
	}
}

// The policy has to be able to fail: each of these workflows breaks it.
func TestReleasePolicyRejectsAnUnconfirmedRelease(t *testing.T) {
	const gate = `
jobs:
  prepare:
    steps:
      - run: |
          [ "$CONFIRM" = "release-go" ] || exit 1
          case "$GITHUB_REF" in refs/tags/v[0-9]*) ;; *) exit 1 ;; esac
      - uses: actions/checkout@v7
  publish:
    needs: [prepare]
    steps:
      - run: docker push
`
	const dispatch = `
on:
  workflow_dispatch:
    inputs:
      confirm:
        required: true
`
	if p := releaseProblems(t, []byte(dispatch+gate)); len(p) != 0 {
		t.Fatalf("a conforming workflow was rejected: %v", p)
	}
	cases := map[string]string{
		"a tag push":                "on:\n  push:\n    tags: ['v*']\n" + gate,
		"a tag push as well":        strings.Replace(dispatch, "on:\n", "on:\n  push:\n    tags: ['v*']\n", 1) + gate,
		"a schedule":                strings.Replace(dispatch, "on:\n", "on:\n  schedule:\n    - cron: '0 0 * * 1'\n", 1) + gate,
		"a pull request":            strings.Replace(dispatch, "on:\n", "on:\n  pull_request:\n", 1) + gate,
		"a release event":           strings.Replace(dispatch, "on:\n", "on:\n  release:\n    types: [published]\n", 1) + gate,
		"no confirm input":          "on:\n  workflow_dispatch:\n" + gate,
		"an optional confirm":       strings.Replace(dispatch, "required: true", "required: false", 1) + gate,
		"checkout first":            dispatch + strings.Replace(gate, "    steps:\n      - run: |", "    steps:\n      - uses: actions/checkout@v7\n      - run: |", 1),
		"no tag check":              dispatch + strings.Replace(gate, "refs/tags/v[0-9]*", "refs/heads/*", 1),
		"a job that skips the gate": dispatch + strings.Replace(gate, "    needs: [prepare]\n", "", 1),
	}
	for name, wf := range cases {
		t.Run(name, func(t *testing.T) {
			if len(releaseProblems(t, []byte(wf))) == 0 {
				t.Errorf("passed the release policy:\n%s", wf)
			}
		})
	}
}
