// paramsdoc renders docs/guacd-parameters.md from the parameter registry
// (operator/pkg/params) so the documentation can never drift from what the
// webhook enforces. Run via `make docs-params` after editing the registry.
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/xorhub/waas/operator/pkg/params"
)

func main() {
	var b strings.Builder
	b.WriteString(`# guacd connection parameters — CR ↔ guacd mapping

<!-- GENERATED FILE — do not edit. Source: operator/pkg/params.
     Regenerate with: make docs-params -->

One vocabulary end to end: the key used in a template's
` + "`spec.protocols[].params`" + ` (and in connect-time overrides) **is** the
guacd wire name. This table is generated from ` + "`operator/pkg/params`" + `,
the registry that the admission webhook, the api-server and the frontend
forms all consume.

## Exposure tiers

| Tier | Meaning |
|---|---|
| ` + "`ui`" + ` | Exposed in portal forms (template editor, workspace creation, connection settings, in-session overlay). |
| ` + "`advanced`" + ` | Same validation policy as ` + "`ui`" + `, but rendered behind the advanced disclosure of its section in every form. |
| ` + "`platform`" + ` | Owned by the platform: injected automatically (hostname, port, credentials) or banned as a security/topology hazard. Rejected by the webhook in any CR, for every caller. |

Each parameter also carries a **category** (display, audio, input,
clipboard, session, security, connection): the thematic section it
renders under in the forms, and the unit a template can delegate
wholesale — a ` + "`userParams`" + ` entry ` + "`cat:audio`" + ` allow-lists every
non-platform parameter of the category for that protocol, including
parameters added to it later (resolved against this registry at
validation time, never hardcoded in the template). Values themselves
are never validated per category.

"Live" parameters can be toggled mid-session (enforced client-side or by
the wwt proxy); everything else requires a reconnect — guacd fixes its
parameters at connect time.

`)
	for _, proto := range params.Protocols() {
		fmt.Fprintf(&b, "## %s\n\n", proto)
		b.WriteString("| Parameter | Category | Tier | Type | Constraints | Default | Live | Description |\n")
		b.WriteString("|---|---|---|---|---|---|---|---|\n")
		for _, p := range params.ForProtocol(proto) {
			constraints := ""
			switch {
			case p.Kind == params.KindEnum:
				constraints = strings.Join(p.Enum, ", ")
			case p.Min != nil && p.Max != nil:
				constraints = fmt.Sprintf("%d – %d", *p.Min, *p.Max)
			case p.Min != nil:
				constraints = fmt.Sprintf(">= %d", *p.Min)
			case p.Max != nil:
				constraints = fmt.Sprintf("<= %d", *p.Max)
			}
			live := ""
			if p.Live {
				live = "yes"
			}
			fmt.Fprintf(&b, "| `%s` | %s | %s | %s | %s | %s | %s | %s |\n",
				p.Name, p.Category, p.Tier, p.Kind, constraints, p.Default, live, p.Description)
		}
		b.WriteString("\n")
	}
	b.WriteString(`## Adding a parameter

1. Add one entry to the registry in ` + "`operator/pkg/params/params.go`" + `:
   name (the guacd wire name), protocols, kind (+ enum/bounds), tier,
   category (the form section it renders under), live flag, description.
   Pick the tier deliberately: security/topology-sensitive parameters
   are ` + "`platform`" + `.
2. Run ` + "`make docs-params`" + ` (this file) and ` + "`make test`" + `
   (the registry has coherence tests).
3. Nothing else. The webhook validates it, the api-server serves it on
   ` + "`GET /api/v1/meta/protocols`" + `, and the forms render it from
   that payload.
`)

	path := "../docs/guacd-parameters.md"
	if len(os.Args) > 1 {
		path = os.Args[1]
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "writing doc:", err)
		os.Exit(1)
	}
	fmt.Println("wrote", path)
}
