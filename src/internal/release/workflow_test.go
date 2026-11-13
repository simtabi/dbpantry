// Package release_test guards how dbpantry publishes. It holds tests only: the
// release workflow is YAML, so there is no Go code here to test, just a policy
// to pin (docs/guides/release.md, design.md D-76).
package release_test

import (
	"os"
	"path/filepath"
	"regexp"
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
	Uses string            `yaml:"uses"`
	Run  string            `yaml:"run"`
	With map[string]string `yaml:"with"`
}

type push struct {
	Tags           []string `yaml:"tags"`
	TagsIgnore     []string `yaml:"tags-ignore"`
	Branches       []string `yaml:"branches"`
	BranchesIgnore []string `yaml:"branches-ignore"`
	Paths          []string `yaml:"paths"`
	PathsIgnore    []string `yaml:"paths-ignore"`
}

type dispatch struct {
	Inputs map[string]struct {
		Required bool   `yaml:"required"`
		Type     string `yaml:"type"`
	} `yaml:"inputs"`
}

// theTag is how every job names the tag being released: the dispatch's tag
// input, or the pushed tag.
const theTag = "${{ inputs.tag || github.ref_name }}"

// imagePublish matches a step or setting that publishes a container image:
// a registry login or push, a multi-architecture manifest, an attestation
// pushed to a registry, an image action, or the permission to write packages.
// Images are no longer published (owner, 2026-11-13); building and scanning
// them locally is not publishing, so `docker build` and `trivy image` pass.
var imagePublish = regexp.MustCompile(`(?m)` +
	`uses:\s*['"]?docker/(login-action|build-push-action|bake-action|metadata-action)\b` +
	`|\bdocker\s+(login|push)\b|\bbuildx\s+build\b[^\n]*--push\b|\bimagetools\s+create\b` +
	`|\b(crane|oras)\s+push\b|\bskopeo\s+copy\b` +
	`|^\s*push-to-registry:\s*true\b|^\s*packages:\s*write\b`)

// comment strips a YAML comment line, so prose explaining what was removed
// does not count as the thing itself.
var comment = regexp.MustCompile(`(?m)^\s*#.*$`)

// releaseProblems returns every way a release workflow breaks the rule: it
// starts only from a pushed version tag or a workflow_dispatch with a required
// tag input; its first job's first step refuses anything but a vX.Y.Z tag that
// exists on GitHub, before any checkout; every other job waits for that job,
// so a refusal publishes nothing; every checkout is of the tag, never a
// branch; and nothing publishes a container image.
func releaseProblems(t *testing.T, src []byte) []string {
	t.Helper()
	var wf workflow
	if err := yaml.Unmarshal(src, &wf); err != nil {
		t.Fatalf("release.yml does not parse: %v", err)
	}
	var problems []string

	for trigger := range wf.On {
		if trigger != "push" && trigger != "workflow_dispatch" {
			problems = append(problems, "trigger "+trigger+": a release runs on a pushed version tag or on request, nothing else")
		}
	}
	if node, ok := wf.On["push"]; !ok {
		problems = append(problems, "no push trigger on version tags")
	} else {
		var p push
		if err := node.Decode(&p); err != nil {
			t.Fatalf("push does not decode: %v", err)
		}
		if len(p.Branches)+len(p.BranchesIgnore)+len(p.Paths)+len(p.PathsIgnore)+len(p.TagsIgnore) > 0 {
			problems = append(problems, "the push trigger filters branches or paths; it must fire on version tags only")
		}
		if len(p.Tags) == 0 {
			problems = append(problems, "the push trigger has no tag filter, so every branch push would release")
		}
		for _, tag := range p.Tags {
			if !strings.HasPrefix(tag, "v[0-9]+.") {
				problems = append(problems, "tag filter "+tag+" is not a version tag")
			}
		}
	}
	if node, ok := wf.On["workflow_dispatch"]; !ok {
		problems = append(problems, "no workflow_dispatch trigger")
	} else {
		var d dispatch
		if err := node.Decode(&d); err != nil {
			t.Fatalf("workflow_dispatch does not decode: %v", err)
		}
		if in, ok := d.Inputs["tag"]; !ok || !in.Required {
			problems = append(problems, "workflow_dispatch lacks a required tag input")
		}
	}
	if !strings.Contains(string(src), theTag) {
		problems = append(problems, "nothing resolves the tag as "+theTag)
	}

	if m := imagePublish.FindString(comment.ReplaceAllString(string(src), "")); m != "" {
		problems = append(problems, "publishes a container image ("+strings.TrimSpace(m)+"); a release publishes the GitHub release only")
	}

	if wf.Jobs.Kind != yaml.MappingNode || len(wf.Jobs.Content) < 2 {
		return append(problems, "release.yml has no jobs")
	}
	jobs := wf.Jobs.Content // key, value, key, value, ...
	first := jobs[0].Value
	checkouts := 0
	for i := 0; i+1 < len(jobs); i += 2 {
		name := jobs[i].Value
		var j job
		if err := jobs[i+1].Decode(&j); err != nil {
			t.Fatalf("job %s does not decode: %v", name, err)
		}
		for _, s := range j.Steps {
			if strings.HasPrefix(s.Uses, "actions/checkout@") {
				checkouts++
				if !strings.HasPrefix(s.With["ref"], "refs/tags/") {
					problems = append(problems, "job "+name+" checks out "+s.With["ref"]+", not refs/tags/<tag>: a dispatch would build its branch")
				}
			}
		}
		if i == 0 {
			if len(j.Steps) == 0 {
				problems = append(problems, "job "+first+" has no steps")
				continue
			}
			s := j.Steps[0]
			if s.Uses != "" {
				problems = append(problems, "the first step of "+first+" is "+s.Uses+"; the refusal must come before any checkout or build")
			}
			for _, want := range []string{`^v[0-9]+\.[0-9]+\.[0-9]+`, "git/ref/tags/", "exit 1"} {
				if !strings.Contains(s.Run, want) {
					problems = append(problems, "the first step of "+first+" must refuse anything but a vX.Y.Z tag on GitHub; it lacks "+want)
				}
			}
			continue
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
	if checkouts == 0 {
		problems = append(problems, "no job checks out the tag")
	}
	return problems
}

// A release runs on a pushed vX.Y.Z tag, or on request for a tag already on
// GitHub (gh workflow run release.yml -f tag=vX.Y.Z), and on nothing else; it
// publishes the GitHub release and no container image (owner, 2026-11-13,
// replacing design.md D-76's confirmed dispatch).
func TestReleaseRunsOnAVersionTagOrOnRequest(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "release.yml")) // #nosec G304 -- a fixed file of this repository
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range releaseProblems(t, src) {
		t.Error("release.yml: " + p)
	}
}

// The policy has to be able to fail: each of these workflows breaks it.
func TestReleasePolicyRejectsAWrongRelease(t *testing.T) {
	const triggers = `
on:
  push:
    tags:
      - 'v[0-9]+.[0-9]+.[0-9]+'
  workflow_dispatch:
    inputs:
      tag:
        required: true
`
	const gate = `
jobs:
  prepare:
    env:
      TAG: ${{ inputs.tag || github.ref_name }}
    steps:
      - run: |
          [[ "$TAG" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || exit 1
          gh api "repos/$GITHUB_REPOSITORY/git/ref/tags/$TAG" --silent || exit 1
      - uses: actions/checkout@v7
        with:
          ref: refs/tags/${{ inputs.tag || github.ref_name }}
  release:
    needs: [prepare]
    permissions:
      contents: write
    steps:
      - uses: actions/checkout@v7
        with:
          ref: refs/tags/${{ needs.prepare.outputs.tag }}
      - run: gh release create "$TAG"
`
	if p := releaseProblems(t, []byte(triggers+gate)); len(p) != 0 {
		t.Fatalf("a conforming workflow was rejected: %v", p)
	}
	withStep := func(s string) string {
		return triggers + gate + "      - " + s + "\n"
	}
	cases := map[string]string{
		"a branch push":             strings.Replace(triggers, "    tags:\n", "    branches: [main]\n    tags:\n", 1) + gate,
		"a push with no tag filter": strings.Replace(triggers, "    tags:\n      - 'v[0-9]+.[0-9]+.[0-9]+'\n", "", 1) + gate,
		"a non-version tag":         strings.Replace(triggers, "'v[0-9]+.[0-9]+.[0-9]+'", "'*'", 1) + gate,
		"no push trigger":           strings.Replace(triggers, "  push:\n    tags:\n      - 'v[0-9]+.[0-9]+.[0-9]+'\n", "", 1) + gate,
		"a schedule":                strings.Replace(triggers, "on:\n", "on:\n  schedule:\n    - cron: '0 0 * * 1'\n", 1) + gate,
		"a pull request":            strings.Replace(triggers, "on:\n", "on:\n  pull_request:\n", 1) + gate,
		"a release event":           strings.Replace(triggers, "on:\n", "on:\n  release:\n    types: [published]\n", 1) + gate,
		"no tag input":              strings.Replace(triggers, "    inputs:\n      tag:\n        required: true\n", "", 1) + gate,
		"an optional tag":           strings.Replace(triggers, "required: true", "required: false", 1) + gate,
		"the retired confirm":       strings.Replace(triggers, "      tag:\n", "      confirm:\n", 1) + gate,
		"checkout first":            triggers + strings.Replace(gate, "    steps:\n      - run: |", "    steps:\n      - uses: actions/checkout@v7\n        with:\n          ref: refs/tags/v1.0.0\n      - run: |", 1),
		"no existence check":        triggers + strings.Replace(gate, "git/ref/tags/", "git/ref/heads/", 1),
		"no version check":          triggers + strings.Replace(gate, `^v[0-9]+\.[0-9]+\.[0-9]+`, `^.+`, 1),
		"a branch checkout":         triggers + strings.Replace(gate, "ref: refs/tags/${{ needs.prepare.outputs.tag }}", "ref: main", 1),
		"a default checkout":        triggers + strings.Replace(gate, "        with:\n          ref: refs/tags/${{ needs.prepare.outputs.tag }}\n", "", 1),
		"a job that skips the gate": triggers + strings.Replace(gate, "    needs: [prepare]\n", "", 1),
		"docker/login-action":       withStep("uses: docker/login-action@v3"),
		"docker/build-push-action":  withStep("uses: docker/build-push-action@v6"),
		"a docker login":            withStep(`run: printf '%s' "$T" | docker login ghcr.io -u x --password-stdin`),
		"a ghcr.io push":            withStep("run: docker push ghcr.io/simtabi/dbpantry-pg:17-0.1.1"),
		"a buildx --push":           withStep("run: docker buildx build -t ghcr.io/simtabi/dbpantry-pg:17 --push ."),
		"a manifest":                withStep("run: docker buildx imagetools create -t ghcr.io/simtabi/dbpantry-pg:17 a b"),
		"an attestation pushed":     withStep("uses: actions/attest@v4\n        with:\n          push-to-registry: true"),
		"packages: write":           strings.Replace(triggers+gate, "      contents: write\n", "      contents: write\n      packages: write\n", 1),
	}
	for name, wf := range cases {
		t.Run(name, func(t *testing.T) {
			if len(releaseProblems(t, []byte(wf))) == 0 {
				t.Errorf("passed the release policy:\n%s", wf)
			}
		})
	}

	// Building and scanning an image locally is not publishing it.
	local := withStep(`run: scripts/build pg && trivy image "ghcr.io/simtabi/dbpantry-pg:17"`)
	if p := releaseProblems(t, []byte(local)); len(p) != 0 {
		t.Fatalf("a local build and scan was rejected: %v", p)
	}
}
