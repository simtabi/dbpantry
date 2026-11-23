// Package envtype is the catalog of deployment environment kinds and the
// registry of named instance homes (design.md D-100).
//
// An "environment" is one MDB_HOME (D-84), not a second stack inside the same
// Compose project. Cross-env copy/migrate uses the existing backup + restore
// / transfer scripts against two homes.
package envtype

import (
	"fmt"
	"slices"
	"strings"
)

// Kind is a stable environment type id (canonical form).
type Kind string

// Canonical kinds. Aliases (prd→prod, tst→test, …) resolve via Parse.
const (
	LocalDev Kind = "localdev"
	Dev      Kind = "dev"
	Test     Kind = "test"
	QA       Kind = "qa"
	UAT      Kind = "uat"
	Train    Kind = "trn"
	Support  Kind = "spt"
	Staging  Kind = "stg"
	PreProd  Kind = "preprod"
	Demo     Kind = "demo"
	Sandbox  Kind = "sandbox"
	Prod     Kind = "prod"
)

// Spec describes one environment kind and the defaults it suggests.
type Spec struct {
	ID      Kind
	Title   string
	Aliases []string
	Summary string
	// ProdLike selects stricter defaults (TLS, pooler, backup encryption notes).
	ProdLike bool
	// Ephemeral hints that data loss on stop is acceptable (test profile).
	Ephemeral bool
	// SuggestOverlays are Compose mode profiles to merge into MDB_PROFILES
	// after init (ui, backup, …). Never list engine names here — check 63.
	SuggestOverlays string
}

// Catalog is every known kind, in a typical promotion order.
func Catalog() []Spec {
	return []Spec{
		{ID: LocalDev, Title: "Local development", Aliases: []string{"local", "lcl"}, Summary: "Developer laptop / single-machine work.", SuggestOverlays: "ui"},
		{ID: Dev, Title: "Development", Aliases: []string{"development", "develop"}, Summary: "Shared team development deploy.", SuggestOverlays: "ui"},
		{ID: Test, Title: "Testing", Aliases: []string{"tst", "testing"}, Summary: "Automated and manual functional tests.", Ephemeral: true},
		{ID: QA, Title: "Quality assurance", Aliases: []string{"quality", "qat"}, Summary: "QA team verification before acceptance.", SuggestOverlays: "ui"},
		{ID: UAT, Title: "User acceptance testing", Aliases: []string{"acceptance", "uatest"}, Summary: "Business / end-user acceptance.", SuggestOverlays: "ui"},
		{ID: Train, Title: "Training", Aliases: []string{"train", "training"}, Summary: "Hands-on training and demos with disposable data.", Ephemeral: true, SuggestOverlays: "ui"},
		{ID: Support, Title: "Support", Aliases: []string{"support", "svc"}, Summary: "Support / hot-fix reproduction.", SuggestOverlays: "ui"},
		{ID: Staging, Title: "Staging", Aliases: []string{"stage", "staging"}, Summary: "Production-like pre-release validation.", ProdLike: true, SuggestOverlays: "ui,backup"},
		{ID: PreProd, Title: "Pre-production", Aliases: []string{"pre-prod", "pre_prod", "pp"}, Summary: "Final dress rehearsal before production.", ProdLike: true, SuggestOverlays: "ui,backup"},
		{ID: Demo, Title: "Demo", Aliases: []string{"demonstration"}, Summary: "Sales and stakeholder demos.", SuggestOverlays: "ui"},
		{ID: Sandbox, Title: "Sandbox", Aliases: []string{"sbx", "play"}, Summary: "Throwaway experiments.", Ephemeral: true},
		{ID: Prod, Title: "Production", Aliases: []string{"prd", "production", "live"}, Summary: "Live traffic. Stricter TLS, pooling and backup defaults.", ProdLike: true, SuggestOverlays: "backup"},
	}
}

// Parse resolves a user token (id or alias) to a Spec.
func Parse(raw string) (Spec, error) {
	t := strings.ToLower(strings.TrimSpace(raw))
	t = strings.ReplaceAll(t, "_", "-")
	if t == "" {
		return Spec{}, fmt.Errorf("environment type is empty")
	}
	for _, s := range Catalog() {
		if string(s.ID) == t {
			return s, nil
		}
		if slices.Contains(s.Aliases, t) {
			return s, nil
		}
	}
	return Spec{}, fmt.Errorf("unknown environment type %q (see dbpantry env types)", raw)
}

// IDs returns every canonical id for docs and Choices.
func IDs() []string {
	c := Catalog()
	out := make([]string, 0, len(c))
	for _, s := range c {
		out = append(out, string(s.ID))
	}
	return out
}
