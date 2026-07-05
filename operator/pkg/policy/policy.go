// Package policy is the single implementation of workspace governance:
// which policy applies to an identity, which catalog images it may use,
// and whether a workspace fits its quotas. The admission webhook, the
// reconciler's re-check and the api-server's catalog/quota endpoints all
// call this package, so IHM, kubectl and reconcile can never disagree.
package policy

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
)

// DefaultHomeSize mirrors the reconciler's default when a template does
// not set homeSize; quota math must count what will actually be
// provisioned.
const DefaultHomeSize = "10Gi"

// Identity is the resolved, trusted identity of a workspace owner.
type Identity struct {
	// Owner is the opaque platform UUID stored in spec.owner.
	Owner string
	// Username is the human identity (OIDC preferred_username/sub).
	Username string
	// Groups are Authentik group names.
	Groups []string
}

// IdentityOf rebuilds the owner identity persisted on a Workspace: the
// spec.owner UUID plus the webhook-guarded identity annotations. For
// workspaces created via direct kubectl the annotations are absent (the
// admission webhook worked from the live request userInfo instead), so
// groups may be empty here — reconcile-time re-checks are then evaluated
// against the ungrouped identity, which is the stricter reading.
func IdentityOf(ws *waasv1alpha1.Workspace) Identity {
	id := Identity{Owner: ws.Spec.Owner, Username: ws.Annotations[waasv1alpha1.AnnotationUsername]}
	if raw := ws.Annotations[waasv1alpha1.AnnotationGroups]; raw != "" {
		for _, g := range strings.Split(raw, ",") {
			if g = strings.TrimSpace(g); g != "" {
				id.Groups = append(id.Groups, g)
			}
		}
	}
	if id.Username == "" {
		id.Username = ws.Spec.Owner
	}
	return id
}

// Reason codes classify denials for status conditions and audit logs.
type Reason string

const (
	ReasonNoPolicy          Reason = "NoPolicyMatches"
	ReasonImageNotInCatalog Reason = "ImageNotInCatalog"
	ReasonImageDisabled     Reason = "ImageDisabled"
	ReasonImageNotAllowed   Reason = "ImageNotAllowed"
	ReasonProtocolMismatch  Reason = "ProtocolMismatch"
	ReasonResourcesInvalid  Reason = "ResourcesOutOfBounds"
	ReasonQuotaExceeded     Reason = "QuotaExceeded"
	ReasonIdentityViolation Reason = "IdentityViolation"
	ReasonInternalError     Reason = "PolicyCheckFailed"
)

// Denial is a policy rejection with an operator-friendly reason code and
// a human message that names the rule and the numbers behind it.
type Denial struct {
	Reason  Reason
	Message string
}

func (d *Denial) Error() string { return d.Message }

func denyf(reason Reason, format string, args ...any) *Denial {
	return &Denial{Reason: reason, Message: fmt.Sprintf(format, args...)}
}

// Resolve picks the policy that governs the identity.
//
// Rule (validated design decision): highest spec.priority among matching
// policies wins and applies AS A WHOLE — no field merging. Ties break on
// the lexicographically smallest name and produce a warning, since two
// same-priority matches are a configuration smell. A policy with no
// subjects matches everyone. No match at all is a Denial: the platform
// fails closed and the fix is to ship a subjects-less "default" policy.
func Resolve(policies []waasv1alpha1.WorkspacePolicy, id Identity) (*waasv1alpha1.WorkspacePolicy, []string, *Denial) {
	var matching []waasv1alpha1.WorkspacePolicy
	for _, p := range policies {
		if subjectsMatch(p.Spec.Subjects, id) {
			matching = append(matching, p)
		}
	}
	if len(matching) == 0 {
		return nil, nil, denyf(ReasonNoPolicy,
			"no WorkspacePolicy matches user %q (groups: %s); an admin must assign one or provide a subjects-less default policy",
			nonEmpty(id.Username, id.Owner), strings.Join(id.Groups, ", "))
	}

	sort.Slice(matching, func(i, j int) bool {
		if matching[i].Spec.Priority != matching[j].Spec.Priority {
			return matching[i].Spec.Priority > matching[j].Spec.Priority
		}
		return matching[i].Name < matching[j].Name
	})

	var warnings []string
	if len(matching) > 1 && matching[0].Spec.Priority == matching[1].Spec.Priority {
		warnings = append(warnings, fmt.Sprintf(
			"policies %q and %q both match with priority %d; %q was chosen by name — give them distinct priorities",
			matching[0].Name, matching[1].Name, matching[0].Spec.Priority, matching[0].Name))
	}
	return &matching[0], warnings, nil
}

func subjectsMatch(subjects []waasv1alpha1.PolicySubject, id Identity) bool {
	if len(subjects) == 0 {
		return true // fallback policy: every authenticated user
	}
	for _, s := range subjects {
		switch s.Kind {
		case waasv1alpha1.SubjectUser:
			// Match either identity facet: the platform UUID (spec.owner)
			// or the human username, so admins can write whichever they
			// see in their tooling.
			if s.Name == id.Owner || (id.Username != "" && s.Name == id.Username) {
				return true
			}
		case waasv1alpha1.SubjectGroup:
			if slices.Contains(id.Groups, s.Name) {
				return true
			}
		}
	}
	return false
}

// ImageAllowed says whether one catalog entry is usable by the identity
// under the given policy, with the denial explaining which gate failed.
func ImageAllowed(img *waasv1alpha1.WorkspaceImage, pol *waasv1alpha1.WorkspacePolicy, id Identity) *Denial {
	if !img.Spec.Enabled {
		return denyf(ReasonImageDisabled, "image %q is disabled by the administrator", img.Name)
	}
	if len(img.Spec.AllowedGroups) > 0 {
		ok := false
		for _, g := range img.Spec.AllowedGroups {
			if slices.Contains(id.Groups, g) {
				ok = true
				break
			}
		}
		if !ok {
			return denyf(ReasonImageNotAllowed,
				"image %q is restricted to groups [%s]; user %q is in [%s]",
				img.Name, strings.Join(img.Spec.AllowedGroups, ", "),
				nonEmpty(id.Username, id.Owner), strings.Join(id.Groups, ", "))
		}
	}
	if len(pol.Spec.Images) > 0 && !slices.Contains(pol.Spec.Images, img.Name) {
		return denyf(ReasonImageNotAllowed,
			"policy %q allows images [%s]; image %q is not among them",
			pol.Name, strings.Join(pol.Spec.Images, ", "), img.Name)
	}
	return nil
}

// AllowedImages filters the catalog down to what the identity may deploy
// — the exact list the portal must display.
func AllowedImages(catalog []waasv1alpha1.WorkspaceImage, pol *waasv1alpha1.WorkspacePolicy, id Identity) []waasv1alpha1.WorkspaceImage {
	var out []waasv1alpha1.WorkspaceImage
	for i := range catalog {
		if ImageAllowed(&catalog[i], pol, id) == nil {
			out = append(out, catalog[i])
		}
	}
	return out
}

// FindImage locates the catalog entry approving an exact image ref, as
// used by a WorkspaceTemplate. Verbatim match only: approving
// "repo:1.0.0" does not approve "repo:1.0.1" or a digest form.
func FindImage(catalog []waasv1alpha1.WorkspaceImage, ref string) *waasv1alpha1.WorkspaceImage {
	for i := range catalog {
		if catalog[i].Spec.Image == ref {
			return &catalog[i]
		}
	}
	return nil
}

// Load is the quota-relevant footprint of one existing workspace.
type Load struct {
	CPU     resource.Quantity
	Memory  resource.Quantity
	Storage resource.Quantity
	Paused  bool
}

// LoadOf computes the footprint the cluster will actually grant a
// workspace: limits if set, else requests, else the image default. The
// bool reports whether compute could be determined at all — callers
// enforcing compute caps must treat "false" as a denial, otherwise a
// size-less workspace would evade every aggregate.
func LoadOf(ws *waasv1alpha1.Workspace, tpl *waasv1alpha1.WorkspaceTemplate, img *waasv1alpha1.WorkspaceImage) (Load, bool) {
	load := Load{Paused: ws.Spec.Paused}

	rr := tpl.Spec.Resources
	if ws.Spec.Resources != nil {
		rr = *ws.Spec.Resources
	}
	cpu, cpuOK := pick(rr, corev1.ResourceCPU)
	mem, memOK := pick(rr, corev1.ResourceMemory)
	if !cpuOK || !memOK {
		if img != nil && img.Spec.Resources != nil && img.Spec.Resources.Default != nil {
			if !cpuOK && img.Spec.Resources.Default.CPU != nil {
				cpu, cpuOK = *img.Spec.Resources.Default.CPU, true
			}
			if !memOK && img.Spec.Resources.Default.Memory != nil {
				mem, memOK = *img.Spec.Resources.Default.Memory, true
			}
		}
	}
	load.CPU, load.Memory = cpu, mem

	size := resource.MustParse(DefaultHomeSize)
	if tpl.Spec.HomeSize != nil {
		size = *tpl.Spec.HomeSize
	}
	load.Storage = size
	return load, cpuOK && memOK
}

func pick(rr corev1.ResourceRequirements, name corev1.ResourceName) (resource.Quantity, bool) {
	if q, ok := rr.Limits[name]; ok {
		return q, true
	}
	if q, ok := rr.Requests[name]; ok {
		return q, true
	}
	return resource.Quantity{}, false
}

// CheckLimits validates one workspace's footprint against the image
// bounds and the policy caps, given the loads of the user's OTHER
// workspaces. Every message carries the numbers that matter, because it
// ends up verbatim in kubectl output, CR conditions and the portal.
func CheckLimits(load Load, computeKnown bool, img *waasv1alpha1.WorkspaceImage, pol *waasv1alpha1.WorkspacePolicy, others []Load) *Denial {
	lim := pol.Spec.Limits

	if lim.MaxWorkspaces != nil && int32(len(others))+1 > *lim.MaxWorkspaces {
		return denyf(ReasonQuotaExceeded,
			"policy %q: workspace quota reached (%d/%d)", pol.Name, len(others), *lim.MaxWorkspaces)
	}

	capsCompute := (lim.PerWorkspace != nil && (lim.PerWorkspace.CPU != nil || lim.PerWorkspace.Memory != nil)) ||
		(lim.Aggregate != nil && (lim.Aggregate.CPU != nil || lim.Aggregate.Memory != nil)) ||
		(img.Spec.Resources != nil && img.Spec.Resources.Max != nil)
	if capsCompute && !computeKnown {
		return denyf(ReasonResourcesInvalid,
			"policy %q caps compute but the workspace declares no cpu/memory (set template or workspace resources, or a default on image %q)",
			pol.Name, img.Name)
	}

	// Per-workspace: effective cap = min(image.max, policy.perWorkspace);
	// image.min guards against undersizing.
	if img.Spec.Resources != nil {
		if m := img.Spec.Resources.Min; m != nil {
			if m.CPU != nil && load.CPU.Cmp(*m.CPU) < 0 {
				return denyf(ReasonResourcesInvalid, "image %q requires at least cpu=%s (got %s)", img.Name, m.CPU, &load.CPU)
			}
			if m.Memory != nil && load.Memory.Cmp(*m.Memory) < 0 {
				return denyf(ReasonResourcesInvalid, "image %q requires at least memory=%s (got %s)", img.Name, m.Memory, &load.Memory)
			}
		}
		if m := img.Spec.Resources.Max; m != nil {
			if m.CPU != nil && load.CPU.Cmp(*m.CPU) > 0 {
				return denyf(ReasonResourcesInvalid, "image %q caps cpu at %s (got %s)", img.Name, m.CPU, &load.CPU)
			}
			if m.Memory != nil && load.Memory.Cmp(*m.Memory) > 0 {
				return denyf(ReasonResourcesInvalid, "image %q caps memory at %s (got %s)", img.Name, m.Memory, &load.Memory)
			}
		}
	}
	if pw := lim.PerWorkspace; pw != nil {
		if pw.CPU != nil && load.CPU.Cmp(*pw.CPU) > 0 {
			return denyf(ReasonResourcesInvalid, "policy %q caps cpu at %s per workspace (got %s)", pol.Name, pw.CPU, &load.CPU)
		}
		if pw.Memory != nil && load.Memory.Cmp(*pw.Memory) > 0 {
			return denyf(ReasonResourcesInvalid, "policy %q caps memory at %s per workspace (got %s)", pol.Name, pw.Memory, &load.Memory)
		}
		if pw.Home != nil && load.Storage.Cmp(*pw.Home) > 0 {
			return denyf(ReasonResourcesInvalid, "policy %q caps home volume at %s (got %s)", pol.Name, pw.Home, &load.Storage)
		}
	}

	// Aggregates: paused workspaces free their compute but keep storage.
	if agg := lim.Aggregate; agg != nil {
		var cpu, mem, sto resource.Quantity
		for _, o := range others {
			if !o.Paused {
				cpu.Add(o.CPU)
				mem.Add(o.Memory)
			}
			sto.Add(o.Storage)
		}
		if !load.Paused {
			cpu.Add(load.CPU)
			mem.Add(load.Memory)
		}
		sto.Add(load.Storage)

		if agg.CPU != nil && cpu.Cmp(*agg.CPU) > 0 {
			return denyf(ReasonQuotaExceeded, "policy %q: total cpu %s would exceed the %s cap", pol.Name, &cpu, agg.CPU)
		}
		if agg.Memory != nil && mem.Cmp(*agg.Memory) > 0 {
			return denyf(ReasonQuotaExceeded, "policy %q: total memory %s would exceed the %s cap", pol.Name, &mem, agg.Memory)
		}
		if agg.Storage != nil && sto.Cmp(*agg.Storage) > 0 {
			return denyf(ReasonQuotaExceeded, "policy %q: total home storage %s would exceed the %s cap", pol.Name, &sto, agg.Storage)
		}
	}
	return nil
}

// CheckProtocol ensures the template's desktop protocol is one the
// catalog entry actually serves.
func CheckProtocol(tpl *waasv1alpha1.WorkspaceTemplate, img *waasv1alpha1.WorkspaceImage) *Denial {
	proto := waasv1alpha1.Protocol(tpl.Spec.Protocol())
	if !slices.Contains(img.Spec.Protocols, proto) {
		return denyf(ReasonProtocolMismatch,
			"template %q uses protocol %q but image %q only serves %v", tpl.Name, proto, img.Name, img.Spec.Protocols)
	}
	return nil
}

func nonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
