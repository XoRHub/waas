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
	"k8s.io/apimachinery/pkg/types"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
	"github.com/xorhub/waas/operator/pkg/naming"
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
	// ReasonImageTagPolicy: the reference violates the matched catalog
	// entry's pinning discipline (tagPolicy digest/tag/any).
	ReasonImageTagPolicy     Reason = "ImageTagPolicy"
	ReasonImageDisabled      Reason = "ImageDisabled"
	ReasonImageNotAllowed    Reason = "ImageNotAllowed"
	ReasonProtocolMismatch   Reason = "ProtocolMismatch"
	ReasonResourcesInvalid   Reason = "ResourcesOutOfBounds"
	ReasonQuotaExceeded      Reason = "QuotaExceeded"
	ReasonIdentityViolation  Reason = "IdentityViolation"
	ReasonOverrideNotAllowed Reason = "OverrideNotAllowed"
	// ReasonPlacementDenied covers target-namespace/workload-name rules:
	// immutability, ownership, and name collisions.
	ReasonPlacementDenied Reason = "PlacementDenied"
	ReasonVolumeDenied    Reason = "VolumeDenied"
	ReasonInternalError   Reason = "PolicyCheckFailed"
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
	return MatchedVia(subjects, id) != ""
}

// MatchedVia explains how a policy's subjects match the identity: the
// matching subject as "Kind:name", "*" for a subjects-less (catch-all)
// policy, or "" when it does not match. Powers the effective-policy debug
// endpoint, so resolution and explanation can never disagree.
func MatchedVia(subjects []waasv1alpha1.PolicySubject, id Identity) string {
	if len(subjects) == 0 {
		return "*" // fallback policy: every authenticated user
	}
	for _, s := range subjects {
		switch s.Kind {
		case waasv1alpha1.SubjectUser:
			// Match either identity facet: the platform UUID (spec.owner)
			// or the human username, so admins can write whichever they
			// see in their tooling.
			if s.Name == id.Owner || (id.Username != "" && s.Name == id.Username) {
				return string(s.Kind) + ":" + s.Name
			}
		case waasv1alpha1.SubjectGroup:
			if slices.Contains(id.Groups, s.Name) {
				return string(s.Kind) + ":" + s.Name
			}
		}
	}
	return ""
}

// ClipboardOf returns the clipboard rights a policy grants: absent policy
// or absent fields mean allowed; a nil policy (no match, fail-closed
// context) means denied.
func ClipboardOf(pol *waasv1alpha1.WorkspacePolicy) (copyFromWorkspace, pasteToWorkspace bool) {
	if pol == nil {
		return false, false
	}
	c := pol.Spec.Clipboard
	if c == nil {
		return true, true
	}
	return c.CopyFromWorkspace == nil || *c.CopyFromWorkspace,
		c.PasteToWorkspace == nil || *c.PasteToWorkspace
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

// FindImage locates the catalog entry approving an image ref, as used by
// a WorkspaceTemplate. An exact entry matches verbatim (approving
// "repo:1.0.0" does not approve "repo:1.0.1" or a digest form) and
// always beats a registry entry; among registry entries the longest
// prefix wins. Registry matching is path-boundary safe:
// "docker.io/kasmweb" matches docker.io/kasmweb/terminal:… but never
// docker.io/kasmweb-evil/*. The tag discipline of the matched entry is
// enforced separately by CheckTagDiscipline.
func FindImage(catalog []waasv1alpha1.WorkspaceImage, ref string) *waasv1alpha1.WorkspaceImage {
	for i := range catalog {
		if catalog[i].Spec.Image != "" && catalog[i].Spec.Image == ref {
			return &catalog[i]
		}
	}
	var best *waasv1alpha1.WorkspaceImage
	for i := range catalog {
		prefix := strings.TrimSuffix(catalog[i].Spec.Registry, "/")
		if prefix == "" || !strings.HasPrefix(ref, prefix+"/") {
			continue
		}
		if best == nil || len(prefix) > len(strings.TrimSuffix(best.Spec.Registry, "/")) {
			best = &catalog[i]
		}
	}
	return best
}

// CheckTagDiscipline enforces the matched entry's tagPolicy on the
// template's reference. Unset defaults differ by entry kind: an EXACT
// entry is a verbatim approval — the admin wrote that precise string,
// :latest included — so it defaults to "any"; a REGISTRY entry approves
// a whole namespace and defaults to "tag" (moving references need the
// explicit tagPolicy: any opt-in — an unpinned image silently changing
// under running templates is exactly what the catalog exists to
// prevent).
func CheckTagDiscipline(img *waasv1alpha1.WorkspaceImage, ref string) *Denial {
	pol := img.Spec.TagPolicy
	if pol == "" {
		if img.Spec.Image != "" {
			pol = waasv1alpha1.TagPolicyAny
		} else {
			pol = waasv1alpha1.TagPolicyTag
		}
	}
	hasDigest := strings.Contains(ref, "@sha256:")
	switch pol {
	case waasv1alpha1.TagPolicyAny:
		return nil
	case waasv1alpha1.TagPolicyDigest:
		if !hasDigest {
			return denyf(ReasonImageTagPolicy,
				"catalog entry %q requires digest-pinned images (@sha256:…); %q has none",
				img.Name, ref)
		}
		return nil
	default: // TagPolicyTag
		if hasDigest {
			return nil
		}
		tag := imageTag(ref)
		if tag == "" || tag == "latest" {
			return denyf(ReasonImageTagPolicy,
				"catalog entry %q requires a fixed tag; %q is a moving reference (set tagPolicy: any to allow it)",
				img.Name, ref)
		}
		return nil
	}
}

// imageTag extracts the tag of a reference, "" when tag-less. The last
// colon only counts when it comes after the last path separator (a
// registry port, e.g. host:5000/repo, is not a tag).
func imageTag(ref string) string {
	if at := strings.Index(ref, "@"); at >= 0 {
		ref = ref[:at]
	}
	slash := strings.LastIndex(ref, "/")
	colon := strings.LastIndex(ref, ":")
	if colon > slash {
		return ref[colon+1:]
	}
	return ""
}

// Load is the quota-relevant footprint of one existing workspace.
type Load struct {
	CPU     resource.Quantity
	Memory  resource.Quantity
	Storage resource.Quantity
	Paused  bool
	// Detached marks a retained volume (a home PVC surviving its deleted
	// workspace): storage-only weight — it counts in the aggregate storage
	// cap but never toward the workspace count nor compute.
	Detached bool
}

// RetainedLoad is the footprint of one retained volume.
func RetainedLoad(size resource.Quantity) Load {
	return Load{Storage: size, Paused: true, Detached: true}
}

// RetainedVolumeLoads converts an owner's retained volumes (PVCs listed
// cluster-wide with LabelRetained + LabelOwner — they live wherever
// their workspace was placed) into storage-only loads. exclude skips the
// volume the workspace under evaluation is ADOPTING (spec.homeVolumeName):
// its weight is the workspace's own load, counting it here would double.
func RetainedVolumeLoads(pvcs []corev1.PersistentVolumeClaim, exclude types.NamespacedName) []Load {
	var out []Load
	for i := range pvcs {
		pvc := &pvcs[i]
		if !pvc.DeletionTimestamp.IsZero() {
			continue
		}
		if pvc.Namespace == exclude.Namespace && pvc.Name == exclude.Name {
			continue
		}
		out = append(out, RetainedLoad(pvc.Spec.Resources.Requests[corev1.ResourceStorage]))
	}
	return out
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

	// Retained volumes weigh on storage only: they are not workspaces.
	workspaceCount := 0
	for _, o := range others {
		if !o.Detached {
			workspaceCount++
		}
	}
	if lim.MaxWorkspaces != nil && int32(workspaceCount)+1 > *lim.MaxWorkspaces {
		return denyf(ReasonQuotaExceeded,
			"policy %q: workspace quota reached (%d/%d)", pol.Name, workspaceCount, *lim.MaxWorkspaces)
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
			return denyf(ReasonQuotaExceeded,
				"policy %q: total home storage %s (retained volumes included) would exceed the %s cap", pol.Name, &sto, agg.Storage)
		}
	}
	return nil
}

// CheckProtocol ensures every protocol the template declares is one the
// catalog entry actually serves.
func CheckProtocol(tpl *waasv1alpha1.WorkspaceTemplate, img *waasv1alpha1.WorkspaceImage) *Denial {
	for _, p := range tpl.Spec.EffectiveProtocols() {
		if !slices.Contains(img.Spec.Protocols, waasv1alpha1.Protocol(p.Name)) {
			return denyf(ReasonProtocolMismatch,
				"template %q uses protocol %q but image %q only serves %v", tpl.Name, p.Name, img.Name, img.Spec.Protocols)
		}
	}
	return nil
}

// PlacementValues builds the pattern placeholder values for one
// workspace: the SINGLE place mapping tokens onto their sources (trusted
// identity, workspace displayName, template name and OS).
func PlacementValues(ws *waasv1alpha1.Workspace, tpl *waasv1alpha1.WorkspaceTemplate, id Identity) naming.PatternValues {
	return naming.PatternValues{
		User:         id.Username,
		Workspace:    ws.Spec.DisplayName,
		TemplateName: tpl.Name,
		OS:           string(tpl.Spec.OS),
	}
}

// ResolvedDefaultNamespace applies the full precedence chain (template
// pattern > operator-wide pattern > built-in) for one workspace.
func ResolvedDefaultNamespace(ws *waasv1alpha1.Workspace, tpl *waasv1alpha1.WorkspaceTemplate, id Identity, globalPattern string) (string, error) {
	pattern := naming.EffectivePattern(tpl.Spec.PlacementNamespacePattern(), globalPattern)
	return naming.ResolveNamespace(pattern, PlacementValues(ws, tpl, id))
}

// CheckOverrides verifies that the creator is entitled to every override
// the workspace carries. Platform admins (role annotation, only trusted
// writers can set it) may override anything. The template owner bypasses
// the template's allow-list but stays subject to the policy's. Everyone
// else needs the field in BOTH lists: the template's allowedFields AND
// the policy's overrides.allowedFields (a nil policy block = no policy
// restriction; pol itself may be nil in policy-less clusters).
// globalPattern is the operator-wide namespace pattern (env), needed to
// tell a platform-resolved default from a user placement override.
func CheckOverrides(ws *waasv1alpha1.Workspace, tpl *waasv1alpha1.WorkspaceTemplate, pol *waasv1alpha1.WorkspacePolicy, id Identity, globalPattern string) *Denial {
	if ws.Annotations[waasv1alpha1.AnnotationRole] == "admin" {
		return nil
	}
	isOwner := tpl.Spec.Overrides != nil && tpl.Spec.Overrides.Owner != "" && tpl.Spec.Overrides.Owner == id.Username

	policyAllows := func(field waasv1alpha1.OverridableField) bool {
		if pol == nil || pol.Spec.Overrides == nil {
			return true
		}
		return slices.Contains(pol.Spec.Overrides.AllowedFields, field)
	}
	checkField := func(field waasv1alpha1.OverridableField) *Denial {
		if !isOwner && !tpl.Spec.FieldOverridable(field) {
			return denyf(ReasonOverrideNotAllowed,
				"template %q does not allow overriding %q (allowed: %v)", tpl.Name, field, allowedFields(tpl))
		}
		if !policyAllows(field) {
			return denyf(ReasonOverrideNotAllowed,
				"policy %q does not allow overriding %q (policy allows: %v)", pol.Name, field, pol.Spec.Overrides.AllowedFields)
		}
		return nil
	}

	// A target namespace deviating from the RESOLVED DEFAULT (template
	// pattern > global pattern > built-in) counts as a "placement"
	// override. Rights only: the webhook enforces separately that any
	// value, allowed or not, belongs to the owner.
	if ws.Spec.TargetNamespace != "" {
		def, _ := ResolvedDefaultNamespace(ws, tpl, id, globalPattern)
		if ws.Spec.TargetNamespace != def {
			if d := checkField(waasv1alpha1.FieldPlacement); d != nil {
				return d
			}
		}
	}
	// Custom sizing: spec.resources PRESENT is the override, whatever its
	// values — even a copy of the template's sizing consumes the right
	// (predictable, and immune to the template changing afterwards). The
	// policy LIMITS keep bounding the values separately.
	if ws.Spec.Resources != nil {
		if d := checkField(waasv1alpha1.FieldResources); d != nil {
			return d
		}
	}

	ov := ws.Spec.Overrides
	if ov == nil {
		return nil
	}
	// What the overrides block uses derives from the claims registry
	// (overrides.go) by reflection — enforcement cannot drift from it.
	for field := range overridesUsage(ov) {
		if d := checkField(field); d != nil {
			return d
		}
	}
	if ov.Protocol != "" && tpl.Spec.ProtocolNamed(ov.Protocol) == nil {
		return denyf(ReasonProtocolMismatch,
			"protocol %q is not declared by template %q", ov.Protocol, tpl.Name)
	}
	return nil
}

// RemoteWorkspacesAllowed reports whether the resolved policy opts its
// users into the Remote Workspaces feature. Nil policy = denied.
func RemoteWorkspacesAllowed(pol *waasv1alpha1.WorkspacePolicy) bool {
	return pol != nil && pol.Spec.RemoteWorkspaces
}

func allowedFields(tpl *waasv1alpha1.WorkspaceTemplate) []waasv1alpha1.OverridableField {
	if tpl.Spec.Overrides == nil {
		return nil
	}
	return tpl.Spec.Overrides.AllowedFields
}

func nonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
