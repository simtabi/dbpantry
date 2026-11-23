package dashboard

import (
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"strings"
	"time"

	"github.com/simtabi/dbpantry/src/internal/endpoint"
)

// actionButton draws the button that runs an action, for every page alike:
//
//	{{actionButton . "service.restart" "Restart" "service=pg" "class=btn-sm" "icon=refresh"}}
//
// Arguments after the label are key=value: class and icon style the button,
// every other key is a parameter the action is started with. The page script
// asks for whatever parameters are missing, and for a confirmation, in one
// modal. An action this principal may not run is drawn disabled with the
// reason as its title (§3.4: disabled with a reason, never hidden). An
// action the page does not know is a template error, so a misspelt name
// fails the page's test rather than drawing a dead button.
func actionButton(p page, name, label string, args ...string) (template.HTML, error) {
	v, ok := p.Actions[name]
	if !ok {
		return "", fmt.Errorf("actionButton: no action %q", name)
	}
	class, ico := "btn", ""
	params := map[string]string{}
	for _, a := range args {
		k, val, ok := strings.Cut(a, "=")
		if !ok {
			return "", fmt.Errorf("actionButton %s: argument %q is not key=value", name, a)
		}
		switch k {
		case "class":
			class += " " + val
		case "icon":
			ico = val
		default:
			params[k] = val
		}
	}
	// Dropdown and list-group actions must not also look like Bootstrap
	// buttons ("btn dropdown-item" / "btn list-group-item" fight the layout).
	menuItem := strings.Contains(class, "dropdown-item") || strings.Contains(class, "list-group-item")
	if menuItem {
		fields := strings.Fields(class)
		out := make([]string, 0, len(fields))
		for _, f := range fields {
			if f == "btn" || strings.HasPrefix(f, "btn-") {
				continue
			}
			out = append(out, f)
		}
		class = strings.Join(out, " ")
	} else if v.Destructive && !strings.Contains(class, "btn-danger") && !strings.Contains(class, "btn-outline-danger") {
		class += " btn-outline-danger"
	}
	if v.Destructive && strings.Contains(class, "dropdown-item") && !strings.Contains(class, "text-danger") {
		class += " text-danger"
	}
	if strings.Contains(class, "list-group-item") && !strings.Contains(class, "d-flex") {
		class += " d-flex align-items-center"
	}
	pj, err := json.Marshal(params)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	inner := html.EscapeString(label)
	if ico != "" {
		if menuItem {
			inner = string(icon(ico)) + `<span class="ms-2">` + inner + `</span>`
		} else {
			inner = string(icon(ico)) + " " + inner
		}
	}
	tip := actionTip(v)
	if !v.Allowed {
		// A disabled button shows no title, so the wrapper carries it.
		fmt.Fprintf(&b, `<span class="d-inline-block" tabindex="0" data-bs-toggle="tooltip" data-bs-placement="top" title="%s">`, html.EscapeString(tip))
		fmt.Fprintf(&b, `<button type="button" class="%s" disabled>%s</button></span>`, html.EscapeString(class), inner)
	} else {
		fmt.Fprintf(&b, `<button type="button" class="%s" data-action="%s" data-params="%s" data-bs-toggle="tooltip" data-bs-placement="top" title="%s">%s</button>`,
			html.EscapeString(class), html.EscapeString(name), html.EscapeString(string(pj)), html.EscapeString(tip), inner)
	}
	// #nosec G203 -- every piece of input is escaped above
	return template.HTML(b.String()), nil
}

// actionsIn lists the actions of one area, in their declared order, for a
// page that offers a whole area (Operations).
func actionsIn(p page, area string) []actionView {
	var out []actionView
	for _, a := range Actions {
		if a.Area == area {
			out = append(out, p.Actions[a.Name])
		}
	}
	return out
}

// area is what the area-card template draws: one area's actions, each with
// its command line and a button.
type area struct {
	P     page
	ID    string
	Title string
	Note  string
	Items []actionView
}

func areaCard(p page, id, title, note string) area {
	return area{P: p, ID: id, Title: title, Note: note, Items: actionsIn(p, id)}
}

// engineCard is one engine on the Engines / Connect pages: its endpoint and
// the page's actions, so the shared context menu can call actionButton.
type engineCard struct {
	P page
	E endpoint.Endpoint
}

// engineMenu builds the value {{template "engine-menu" (engineMenu $ .)}} needs.
func engineMenu(p page, e endpoint.Endpoint) engineCard {
	return engineCard{P: p, E: e}
}

// projectCard is one projects-table row's menu: the page plus the project.
type projectCard struct {
	P page
	R Project
}

// projectMenu builds the value {{template "project-menu" (projectMenu $ .)}} needs.
func projectMenu(p page, r Project) projectCard {
	return projectCard{P: p, R: r}
}

// serviceCard is one Services-table row's ⋮ menu.
type serviceCard struct {
	P page
	S Service
}

// serviceMenu builds {{template "service-menu" (serviceMenu $ .)}}.
func serviceMenu(p page, s Service) serviceCard {
	return serviceCard{P: p, S: s}
}

// commandLine is an action's command as the modal and the area cards show
// it, with each parameter as a placeholder.
func commandLine(a Action) string {
	v := Values{}
	for _, p := range a.Params {
		v[p.Name] = "<" + p.Name + ">"
	}
	return "dbpantry " + strings.Join(a.Command(v), " ")
}

// actionTip is the tooltip / popover text for an action button: what it
// does, the command it runs, and whether confirmation is required.
func actionTip(v actionView) string {
	if !v.Allowed && v.Reason != "" {
		return v.Reason
	}
	tip := v.Title + " — runs " + v.Command
	switch {
	case v.Destructive:
		tip += ". Requires typing a confirmation value (same check as the CLI)."
	case v.Confirm:
		tip += ". Asks for confirmation before running."
	default:
		tip += ". Streams progress into the job drawer."
	}
	return tip
}

// ms prints an audit entry's duration.
func ms(n int64) string {
	d := time.Duration(n) * time.Millisecond
	switch {
	case d < time.Second:
		return fmt.Sprintf("%d ms", n)
	case d < time.Minute:
		return fmt.Sprintf("%.1f s", d.Seconds())
	default:
		return d.Round(time.Second).String()
	}
}

// profilesWith returns current profiles as a CSV, appending add when it is
// not already present. Used by stack.up "Start with <engine>" buttons so
// print/join spacing cannot corrupt the profiles= parameter.
func profilesWith(current []string, add string) string {
	seen := map[string]bool{}
	out := make([]string, 0, len(current)+1)
	for _, p := range current {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	add = strings.TrimSpace(add)
	if add != "" && !seen[add] {
		out = append(out, add)
	}
	return strings.Join(out, ",")
}
