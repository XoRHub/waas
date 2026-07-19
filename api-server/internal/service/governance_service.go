package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
	"github.com/xorhub/waas/operator/pkg/params"
	"github.com/xorhub/waas/operator/pkg/policy"

	"github.com/xorhub/waas/api-server/internal/apierror"
	"github.com/xorhub/waas/api-server/internal/model"
	"github.com/xorhub/waas/api-server/internal/repository"
)

// GovernanceService projects the WorkspaceImage/WorkspacePolicy CRDs into
// the portal: the user-facing catalog+quota views and the admin CRUD.
// It reuses the operator's pkg/policy evaluator, so what the portal
// displays is by construction what the webhook will decide.
//
// Admin writes go straight to the Kubernetes API (validated design
// decision): the CRDs are the source of truth, ArgoCD only bootstraps
// the initial catalog/policies and must not self-heal them.
type GovernanceService struct {
	kube      client.Client
	namespace string
	users     repository.UserRepository
	audit     *AuditService
	catalog   repository.CatalogRepository
	syncer    *CatalogSyncWorker
}

// NewGovernanceService wires the governance projections.
func NewGovernanceService(kube client.Client, namespace string, users repository.UserRepository, audit *AuditService, catalog repository.CatalogRepository) *GovernanceService {
	return &GovernanceService{kube: kube, namespace: namespace, users: users, audit: audit, catalog: catalog}
}

// WithCatalogSyncer enables the admin force-sync endpoint by sharing
// the sync worker — sharing (not a second instance) is what makes the
// worker's mutex actually serialize manual syncs with the ticker.
func (s *GovernanceService) WithCatalogSyncer(w *CatalogSyncWorker) *GovernanceService {
	s.syncer = w
	return s
}

// identityFor resolves the platform identity of a user record, matching
// exactly what the api-server stamps on Workspace CRs.
func identityFor(u *model.User) policy.Identity {
	return policy.Identity{Owner: u.ID, Username: u.Username, Groups: u.Groups}
}

func (s *GovernanceService) fetchAll(ctx context.Context) (*waasv1alpha1.WorkspaceImageList, *waasv1alpha1.WorkspacePolicyList, error) {
	images := &waasv1alpha1.WorkspaceImageList{}
	if err := s.kube.List(ctx, images, client.InNamespace(s.namespace)); err != nil {
		return nil, nil, fmt.Errorf("listing workspace images: %w", err)
	}
	policies := &waasv1alpha1.WorkspacePolicyList{}
	if err := s.kube.List(ctx, policies, client.InNamespace(s.namespace)); err != nil {
		return nil, nil, fmt.Errorf("listing workspace policies: %w", err)
	}
	return images, policies, nil
}

// Catalog returns the images the actor may deploy — the exact set the
// admission webhook would accept, plus the templates that use each image.
func (s *GovernanceService) Catalog(ctx context.Context, actor Actor) ([]model.CatalogImage, error) {
	user, err := s.users.FindByID(ctx, actor.ID)
	if err != nil {
		if errors.Is(err, repository.ErrUserNotFound) {
			return nil, apierror.NotFound("user not found")
		}
		return nil, err
	}
	images, policies, err := s.fetchAll(ctx)
	if err != nil {
		return nil, err
	}

	id := identityFor(user)
	pol, _, denial := policy.Resolve(policies.Items, id)
	if denial != nil {
		// No policy: empty catalog, not an error — the portal shows
		// "no images available, contact your administrator".
		return []model.CatalogImage{}, nil
	}

	templates := &waasv1alpha1.WorkspaceTemplateList{}
	if err := s.kube.List(ctx, templates, client.InNamespace(s.namespace)); err != nil {
		return nil, fmt.Errorf("listing templates: %w", err)
	}
	// Registry entries approve by prefix, so the template→entry
	// association must use the same matcher as enforcement (exact entry
	// wins, then longest registry prefix), keyed by entry name.
	templatesByEntry := map[string][]string{}
	for i := range templates.Items {
		t := &templates.Items[i]
		if entry := policy.FindImage(images.Items, t.Spec.Image); entry != nil {
			templatesByEntry[entry.Name] = append(templatesByEntry[entry.Name], t.Name)
		}
	}

	allowed := policy.AllowedImages(images.Items, pol, id)
	out := make([]model.CatalogImage, 0, len(allowed))
	for i := range allowed {
		m, err := s.imageToModel(ctx, &allowed[i])
		if err != nil {
			return nil, err
		}
		m.Templates = templatesByEntry[allowed[i].Name]
		out = append(out, m)
	}
	return out, nil
}

// Quota returns the actor's applied policy, limits and live consumption.
func (s *GovernanceService) Quota(ctx context.Context, actor Actor) (*model.QuotaStatus, error) {
	user, err := s.users.FindByID(ctx, actor.ID)
	if err != nil {
		if errors.Is(err, repository.ErrUserNotFound) {
			return nil, apierror.NotFound("user not found")
		}
		return nil, err
	}
	images, policies, err := s.fetchAll(ctx)
	if err != nil {
		return nil, err
	}

	id := identityFor(user)
	pol, _, denial := policy.Resolve(policies.Items, id)
	isAdmin := string(user.Role) == "admin"
	if denial != nil {
		// Fail-closed for self-service — but admins keep their feature
		// flags (they bypass policy gates everywhere else too).
		return &model.QuotaStatus{Policy: "", Features: featureFlags(nil, isAdmin)}, nil
	}

	use, err := s.usageOf(ctx, user.ID, images.Items)
	if err != nil {
		return nil, err
	}

	status := &model.QuotaStatus{
		Policy:               pol.Name,
		PolicyPriority:       pol.Spec.Priority,
		MaxWorkspaces:        pol.Spec.Limits.MaxWorkspaces,
		UsedWorkspaces:       use.workspaces,
		MaxRunningWorkspaces: pol.Spec.Limits.MaxRunningWorkspaces,
		RunningWorkspaces:    use.running,
		Used:                 use.used,
		RetainedVolumes:      use.retainedVolumes,
		RetainedStorage:      use.retainedStorage,
		Features:             featureFlags(pol, isAdmin),
	}
	if pol.Spec.Overrides != nil {
		status.AllowedOverrides = []string{}
		for _, f := range pol.Spec.Overrides.AllowedFields {
			status.AllowedOverrides = append(status.AllowedOverrides, string(f))
		}
	}
	if agg := pol.Spec.Limits.Aggregate; agg != nil {
		status.Limits = map[string]string{}
		if agg.CPU != nil {
			status.Limits["cpu"] = agg.CPU.String()
		}
		if agg.Memory != nil {
			status.Limits["memory"] = agg.Memory.String()
		}
		if agg.Storage != nil {
			status.Limits["storage"] = agg.Storage.String()
		}
	}
	if pw := pol.Spec.Limits.PerWorkspace; pw != nil {
		status.PerWorkspace = map[string]string{}
		if pw.CPU != nil {
			status.PerWorkspace["cpu"] = pw.CPU.String()
		}
		if pw.Memory != nil {
			status.PerWorkspace["memory"] = pw.Memory.String()
		}
		if pw.Home != nil {
			status.PerWorkspace["home"] = pw.Home.String()
		}
	}
	status.Defaults = sizeToMap(pol.Spec.Limits.Defaults)
	if lc := pol.Spec.Lifecycle; lc != nil {
		status.Lifecycle = map[string]string{}
		if lc.IdleSuspendAfter != nil {
			status.Lifecycle["idleSuspendAfter"] = lc.IdleSuspendAfter.Duration.String()
		}
		if lc.MaxLifetime != nil {
			status.Lifecycle["maxLifetime"] = lc.MaxLifetime.Duration.String()
		}
	}
	return status, nil
}

// featureFlags projects the policy's opt-in features; admins get them
// all regardless of policy.
func featureFlags(pol *waasv1alpha1.WorkspacePolicy, isAdmin bool) map[string]bool {
	return map[string]bool{
		"remoteWorkspaces": isAdmin || policy.RemoteWorkspacesAllowed(pol),
	}
}

// usage is one owner's live consumption (same math as pkg/policy — the
// displayed numbers can never diverge from what the webhook enforces).
type usage struct {
	workspaces int
	// running excludes paused workspaces and retained volumes — the
	// denominator of the maxRunningWorkspaces limit.
	running int
	used    map[string]string
	// retained volumes: already inside used["storage"], broken out so the
	// UI can say "dont X Gi conservés".
	retainedVolumes int
	retainedStorage string
}

// usageOf computes one owner's live aggregates (same math as pkg/policy).
// Retained volumes weigh on storage exactly as in the admission webhook.
func (s *GovernanceService) usageOf(ctx context.Context, ownerID string, catalog []waasv1alpha1.WorkspaceImage) (usage, error) {
	all := &waasv1alpha1.WorkspaceList{}
	if err := s.kube.List(ctx, all, client.InNamespace(s.namespace)); err != nil {
		return usage{}, fmt.Errorf("listing workspaces: %w", err)
	}
	templates := &waasv1alpha1.WorkspaceTemplateList{}
	if err := s.kube.List(ctx, templates, client.InNamespace(s.namespace)); err != nil {
		return usage{}, fmt.Errorf("listing templates: %w", err)
	}
	// ONE templates LIST + the shared pkg/policy.OwnerLoads: this used to
	// be a per-workspace GET (an N+1 re-run by the 15s quota poll) with a
	// locally diverged vanished-template fallback that undercounted
	// storage versus the enforcement.
	loads := policy.OwnerLoads(all.Items, ownerID, "", policy.TemplatesByName(templates.Items), catalog)
	retained := &corev1.PersistentVolumeClaimList{}
	if err := s.kube.List(ctx, retained, client.MatchingLabels{
		waasv1alpha1.LabelRetained: "true",
		waasv1alpha1.LabelOwner:    ownerID,
	}); err != nil {
		return usage{}, fmt.Errorf("listing retained volumes: %w", err)
	}
	loads = append(loads, policy.RetainedVolumeLoads(retained.Items, types.NamespacedName{})...)

	var cpu, mem, sto, retainedSto = newQty(), newQty(), newQty(), newQty()
	out := usage{}
	for _, l := range loads {
		if !l.Paused {
			cpu.Add(l.CPU)
			mem.Add(l.Memory)
		}
		sto.Add(l.Storage)
		if l.Detached {
			out.retainedVolumes++
			retainedSto.Add(l.Storage)
		} else {
			out.workspaces++
			if !l.Paused {
				out.running++
			}
		}
	}
	out.used = map[string]string{
		"cpu": cpu.String(), "memory": mem.String(), "storage": sto.String(),
	}
	out.retainedStorage = retainedSto.String()
	return out, nil
}

// ---------------------------------------------------------------- admin

// AdminListImages returns the whole catalog, disabled entries included.
func (s *GovernanceService) AdminListImages(ctx context.Context) ([]model.CatalogImage, error) {
	images := &waasv1alpha1.WorkspaceImageList{}
	if err := s.kube.List(ctx, images, client.InNamespace(s.namespace)); err != nil {
		return nil, fmt.Errorf("listing workspace images: %w", err)
	}
	out := make([]model.CatalogImage, 0, len(images.Items))
	for i := range images.Items {
		m, err := s.imageToModel(ctx, &images.Items[i])
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

// UpsertImageInput is the admin payload for a catalog entry.
type UpsertImageInput struct {
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
	// Exactly one of image (exact reference) / registry (prefix
	// approving everything under it) must be set.
	Image    string `json:"image,omitempty"`
	Registry string `json:"registry,omitempty"`
	// TagPolicy: digest | tag (default — :latest rejected) | any.
	TagPolicy string `json:"tagPolicy,omitempty"`
	// ImagePullSecretRef names an existing dockerconfigjson Secret (in
	// the platform workspace namespace) for this entry's registry.
	ImagePullSecretRef string            `json:"imagePullSecretRef,omitempty"`
	Protocols          []string          `json:"protocols"`
	Architectures      []string          `json:"architectures"`
	Enabled            *bool             `json:"enabled"`
	AllowedGroups      []string          `json:"allowedGroups"`
	Defaults           map[string]string `json:"defaults"`
	Min                map[string]string `json:"min"`
	Max                map[string]string `json:"max"`
}

// AdminUpsertImage creates or updates a WorkspaceImage.
func (s *GovernanceService) AdminUpsertImage(ctx context.Context, actor Actor, name string, in UpsertImageInput) (*model.CatalogImage, error) {
	if name == "" || in.DisplayName == "" || len(in.Protocols) == 0 {
		return nil, apierror.BadRequest("name, displayName and protocols are required")
	}
	if (in.Image == "") == (in.Registry == "") {
		return nil, apierror.BadRequest("exactly one of image (exact reference) or registry (approved prefix) must be set")
	}
	switch waasv1alpha1.ImageTagPolicy(in.TagPolicy) {
	case "", waasv1alpha1.TagPolicyDigest, waasv1alpha1.TagPolicyTag, waasv1alpha1.TagPolicyAny:
	default:
		return nil, apierror.BadRequest("tagPolicy must be one of digest, tag, any")
	}
	spec := waasv1alpha1.WorkspaceImageSpec{
		DisplayName:        in.DisplayName,
		Description:        in.Description,
		Image:              in.Image,
		Registry:           strings.TrimSuffix(in.Registry, "/"),
		TagPolicy:          waasv1alpha1.ImageTagPolicy(in.TagPolicy),
		Enabled:            in.Enabled == nil || *in.Enabled,
		AllowedGroups:      in.AllowedGroups,
		ImagePullSecretRef: in.ImagePullSecretRef,
		Architectures:      in.Architectures,
	}
	for _, p := range in.Protocols {
		// params.Protocols() is the single source of protocol names — a
		// hand-written switch here was the 4th copy of the list and
		// nearly missed the kasmvnc addition.
		if !slices.Contains(params.Protocols(), p) {
			return nil, apierror.BadRequest(fmt.Sprintf("unknown protocol %q (must be one of %v)", p, params.Protocols()))
		}
		spec.Protocols = append(spec.Protocols, waasv1alpha1.Protocol(p))
	}
	res, err := computeSizes(in.Defaults, in.Min, in.Max)
	if err != nil {
		return nil, err
	}
	spec.Resources = res

	img := &waasv1alpha1.WorkspaceImage{}
	err = s.kube.Get(ctx, client.ObjectKey{Namespace: s.namespace, Name: name}, img)
	switch {
	case apierrors.IsNotFound(err):
		img = &waasv1alpha1.WorkspaceImage{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: s.namespace},
			Spec:       spec,
		}
		if err := s.kube.Create(ctx, img); err != nil {
			return nil, fmt.Errorf("creating workspace image %s: %w", name, err)
		}
		s.audit.Record(ctx, actor, "catalog.image_created", "workspaceimage", name, "image="+in.Image)
	case err != nil:
		return nil, fmt.Errorf("fetching workspace image %s: %w", name, err)
	default:
		img.Spec = spec
		if err := s.kube.Update(ctx, img); err != nil {
			return nil, fmt.Errorf("updating workspace image %s: %w", name, err)
		}
		s.audit.Record(ctx, actor, "catalog.image_updated", "workspaceimage", name,
			fmt.Sprintf("enabled=%t image=%s", spec.Enabled, in.Image))
	}
	m, err := s.imageToModel(ctx, img)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// AdminSetImageEnabled is the one-click kill switch.
func (s *GovernanceService) AdminSetImageEnabled(ctx context.Context, actor Actor, name string, enabled bool) (*model.CatalogImage, error) {
	img := &waasv1alpha1.WorkspaceImage{}
	if err := s.kube.Get(ctx, client.ObjectKey{Namespace: s.namespace, Name: name}, img); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, apierror.NotFound("catalog image not found")
		}
		return nil, fmt.Errorf("fetching workspace image %s: %w", name, err)
	}
	img.Spec.Enabled = enabled
	if err := s.kube.Update(ctx, img); err != nil {
		return nil, fmt.Errorf("updating workspace image %s: %w", name, err)
	}
	s.audit.Record(ctx, actor, "catalog.image_toggled", "workspaceimage", name, fmt.Sprintf("enabled=%t", enabled))
	m, err := s.imageToModel(ctx, img)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// AdminSyncImage forces an immediate catalog re-fetch of one entry —
// synchronous (bounded by catalogFetchTimeout) so the response carries
// the fresh status and discovered entries. Failure keeps the fail-soft
// doctrine: entries stay stale-but-served, only lastSyncError is
// patched, and the fetch error comes back as a 502 problem.
func (s *GovernanceService) AdminSyncImage(ctx context.Context, actor Actor, name string) (*model.CatalogImage, error) {
	img := &waasv1alpha1.WorkspaceImage{}
	if err := s.kube.Get(ctx, client.ObjectKey{Namespace: s.namespace, Name: name}, img); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, apierror.NotFound("catalog image not found")
		}
		return nil, fmt.Errorf("fetching workspace image %s: %w", name, err)
	}
	// Same eligibility gate as the ticker's syncAll.
	if img.Spec.Registry == "" || img.Spec.Catalog == nil {
		return nil, apierror.BadRequest("image has no catalog source (spec.catalog)")
	}
	if s.syncer == nil {
		return nil, apierror.Unavailable("catalog sync is not available")
	}
	if err := s.syncer.SyncNow(ctx, img); err != nil {
		s.audit.Record(ctx, actor, "catalog.image_synced", "workspaceimage", name, "error="+err.Error())
		return nil, apierror.BadGateway("catalog sync failed: " + err.Error())
	}
	s.audit.Record(ctx, actor, "catalog.image_synced", "workspaceimage", name, "")
	m, err := s.imageToModel(ctx, img)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// AdminDeleteImage removes a catalog entry entirely (prefer disabling).
func (s *GovernanceService) AdminDeleteImage(ctx context.Context, actor Actor, name string) error {
	img := &waasv1alpha1.WorkspaceImage{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: s.namespace}}
	if err := s.kube.Delete(ctx, img); err != nil {
		if apierrors.IsNotFound(err) {
			return apierror.NotFound("catalog image not found")
		}
		return fmt.Errorf("deleting workspace image %s: %w", name, err)
	}
	s.audit.Record(ctx, actor, "catalog.image_deleted", "workspaceimage", name, "")
	return nil
}

// AdminListPolicies returns every WorkspacePolicy.
func (s *GovernanceService) AdminListPolicies(ctx context.Context) ([]model.PolicyModel, error) {
	policies := &waasv1alpha1.WorkspacePolicyList{}
	if err := s.kube.List(ctx, policies, client.InNamespace(s.namespace)); err != nil {
		return nil, fmt.Errorf("listing workspace policies: %w", err)
	}
	out := make([]model.PolicyModel, 0, len(policies.Items))
	for i := range policies.Items {
		out = append(out, policyToModel(&policies.Items[i]))
	}
	return out, nil
}

// UpsertPolicyInput is the admin payload for a policy.
type UpsertPolicyInput struct {
	Priority         int32                       `json:"priority"`
	Subjects         []model.PolicySubject       `json:"subjects"`
	Images           []string                    `json:"images"`
	Limits           model.PolicyLimitsModel     `json:"limits"`
	Lifecycle        map[string]string           `json:"lifecycle"`
	Clipboard        *model.ClipboardPolicyModel `json:"clipboard"`
	Overrides        *model.PolicyOverridesModel `json:"overrides"`
	RemoteWorkspaces bool                        `json:"remoteWorkspaces"`
}

// AdminUpsertPolicy creates or updates a WorkspacePolicy.
func (s *GovernanceService) AdminUpsertPolicy(ctx context.Context, actor Actor, name string, in UpsertPolicyInput) (*model.PolicyModel, error) {
	if name == "" {
		return nil, apierror.BadRequest("policy name is required")
	}
	spec := waasv1alpha1.WorkspacePolicySpec{Priority: in.Priority, Images: in.Images}
	for _, sub := range in.Subjects {
		kind := waasv1alpha1.SubjectKind(sub.Kind)
		if kind != waasv1alpha1.SubjectUser && kind != waasv1alpha1.SubjectGroup {
			return nil, apierror.BadRequest(fmt.Sprintf("subject kind must be User or Group, got %q", sub.Kind))
		}
		spec.Subjects = append(spec.Subjects, waasv1alpha1.PolicySubject{Kind: kind, Name: sub.Name})
	}
	spec.Limits.MaxWorkspaces = in.Limits.MaxWorkspaces
	spec.Limits.MaxRunningWorkspaces = in.Limits.MaxRunningWorkspaces
	pw, err := parseCaps(in.Limits.PerWorkspace, "perWorkspace")
	if err != nil {
		return nil, err
	}
	if pw != nil {
		spec.Limits.PerWorkspace = &waasv1alpha1.PerWorkspaceCaps{CPU: pw["cpu"], Memory: pw["memory"], Home: pw["home"]}
	}
	agg, err := parseCaps(in.Limits.Aggregate, "aggregate")
	if err != nil {
		return nil, err
	}
	if agg != nil {
		spec.Limits.Aggregate = &waasv1alpha1.AggregateCaps{CPU: agg["cpu"], Memory: agg["memory"], Storage: agg["storage"]}
	}
	defaults, err := parseSize(in.Limits.Defaults, "limits.defaults")
	if err != nil {
		return nil, err
	}
	spec.Limits.Defaults = defaults
	if len(in.Lifecycle) > 0 {
		lc := &waasv1alpha1.PolicyLifecycle{}
		if v := in.Lifecycle["idleSuspendAfter"]; v != "" {
			d, err := time.ParseDuration(v)
			if err != nil {
				return nil, apierror.BadRequest("lifecycle.idleSuspendAfter: invalid duration")
			}
			lc.IdleSuspendAfter = &metav1.Duration{Duration: d}
		}
		if v := in.Lifecycle["maxLifetime"]; v != "" {
			d, err := time.ParseDuration(v)
			if err != nil {
				return nil, apierror.BadRequest("lifecycle.maxLifetime: invalid duration")
			}
			lc.MaxLifetime = &metav1.Duration{Duration: d}
		}
		spec.Lifecycle = lc
	}
	if in.Clipboard != nil {
		spec.Clipboard = &waasv1alpha1.ClipboardPolicy{
			CopyFromWorkspace: in.Clipboard.CopyFromWorkspace,
			PasteToWorkspace:  in.Clipboard.PasteToWorkspace,
		}
	}
	if in.Overrides != nil {
		ov := &waasv1alpha1.PolicyOverrides{}
		for _, f := range in.Overrides.AllowedFields {
			field := waasv1alpha1.OverridableField(f)
			if !slices.Contains(waasv1alpha1.AllOverridableFields(), field) {
				return nil, apierror.BadRequest(fmt.Sprintf("overrides.allowedFields: unknown field %q (known: %v)",
					f, waasv1alpha1.AllOverridableFields()))
			}
			ov.AllowedFields = append(ov.AllowedFields, field)
		}
		spec.Overrides = ov
	}
	spec.RemoteWorkspaces = in.RemoteWorkspaces

	pol := &waasv1alpha1.WorkspacePolicy{}
	err = s.kube.Get(ctx, client.ObjectKey{Namespace: s.namespace, Name: name}, pol)
	switch {
	case apierrors.IsNotFound(err):
		pol = &waasv1alpha1.WorkspacePolicy{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: s.namespace},
			Spec:       spec,
		}
		if err := s.kube.Create(ctx, pol); err != nil {
			return nil, fmt.Errorf("creating workspace policy %s: %w", name, err)
		}
		s.audit.Record(ctx, actor, "policy.created", "workspacepolicy", name, "")
	case err != nil:
		return nil, fmt.Errorf("fetching workspace policy %s: %w", name, err)
	default:
		pol.Spec = spec
		if err := s.kube.Update(ctx, pol); err != nil {
			return nil, fmt.Errorf("updating workspace policy %s: %w", name, err)
		}
		s.audit.Record(ctx, actor, "policy.updated", "workspacepolicy", name, "")
	}
	m := policyToModel(pol)
	return &m, nil
}

// AdminDeletePolicy removes a policy. Users it governed fall through to
// the next matching policy — or to fail-closed if none remains.
func (s *GovernanceService) AdminDeletePolicy(ctx context.Context, actor Actor, name string) error {
	pol := &waasv1alpha1.WorkspacePolicy{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: s.namespace}}
	if err := s.kube.Delete(ctx, pol); err != nil {
		if apierrors.IsNotFound(err) {
			return apierror.NotFound("policy not found")
		}
		return fmt.Errorf("deleting workspace policy %s: %w", name, err)
	}
	s.audit.Record(ctx, actor, "policy.deleted", "workspacepolicy", name, "")
	return nil
}

// AdminEffectivePolicy is the debug view behind "why is this user stuck on
// that policy": it replays the exact resolution the webhook performs and
// reports every policy's match outcome.
func (s *GovernanceService) AdminEffectivePolicy(ctx context.Context, userID string) (*model.EffectivePolicy, error) {
	user, err := s.users.FindByID(ctx, userID)
	if err != nil {
		if errors.Is(err, repository.ErrUserNotFound) {
			return nil, apierror.NotFound("user not found")
		}
		return nil, err
	}
	policies := &waasv1alpha1.WorkspacePolicyList{}
	if err := s.kube.List(ctx, policies, client.InNamespace(s.namespace)); err != nil {
		return nil, fmt.Errorf("listing workspace policies: %w", err)
	}

	id := identityFor(user)
	out := &model.EffectivePolicy{
		UserID:   user.ID,
		Username: user.Username,
		// Both non-nil guaranteed (see the model markers): the debug
		// report's consumers iterate them unguarded.
		Groups:    append([]string{}, user.Groups...),
		Evaluated: []model.EvaluatedPolicy{},
	}
	pol, warnings, denial := policy.Resolve(policies.Items, id)
	out.Warnings = warnings
	if denial != nil {
		out.Denial = denial.Message
	} else {
		m := policyToModel(pol)
		out.Effective = &m
	}

	// Same order the resolution sorts by: priority desc, then name.
	items := make([]waasv1alpha1.WorkspacePolicy, len(policies.Items))
	copy(items, policies.Items)
	sort.Slice(items, func(i, j int) bool {
		if items[i].Spec.Priority != items[j].Spec.Priority {
			return items[i].Spec.Priority > items[j].Spec.Priority
		}
		return items[i].Name < items[j].Name
	})
	for i := range items {
		via := policy.MatchedVia(items[i].Spec.Subjects, id)
		out.Evaluated = append(out.Evaluated, model.EvaluatedPolicy{
			Name:     items[i].Name,
			Priority: items[i].Spec.Priority,
			Matched:  via != "",
			Via:      via,
			Selected: pol != nil && pol.Name == items[i].Name,
		})
	}
	return out, nil
}

// AdminKnownGroups lists the group names the platform already knows: the
// Group subjects of every policy plus the groups mirrored onto existing
// users. These are IdP group names (from the OIDC claim / admin
// edits) — the source the admin picks from when creating a user. Not an
// exhaustive IdP directory: it surfaces the groups that actually
// matter here (those a policy targets or a user already has).
func (s *GovernanceService) AdminKnownGroups(ctx context.Context) ([]string, error) {
	set := map[string]bool{}
	policies := &waasv1alpha1.WorkspacePolicyList{}
	if err := s.kube.List(ctx, policies, client.InNamespace(s.namespace)); err != nil {
		return nil, fmt.Errorf("listing workspace policies: %w", err)
	}
	for i := range policies.Items {
		for _, sub := range policies.Items[i].Spec.Subjects {
			if sub.Kind == waasv1alpha1.SubjectGroup && sub.Name != "" {
				set[sub.Name] = true
			}
		}
	}
	// Page through users to collect their mirrored groups.
	for page := 1; ; page++ {
		users, total, err := s.users.List(ctx, page, 200)
		if err != nil {
			return nil, fmt.Errorf("listing users: %w", err)
		}
		for i := range users {
			for _, g := range users[i].Groups {
				if g != "" {
					set[g] = true
				}
			}
		}
		if page*200 >= total || len(users) == 0 {
			break
		}
	}
	out := make([]string, 0, len(set))
	for g := range set {
		out = append(out, g)
	}
	sort.Strings(out)
	return out, nil
}

// AdminUsage is the consumption view: one row per user that owns at
// least one workspace, with the policy currently governing them.
func (s *GovernanceService) AdminUsage(ctx context.Context) ([]model.UserUsage, error) {
	images, policies, err := s.fetchAll(ctx)
	if err != nil {
		return nil, err
	}
	all := &waasv1alpha1.WorkspaceList{}
	if err := s.kube.List(ctx, all, client.InNamespace(s.namespace)); err != nil {
		return nil, fmt.Errorf("listing workspaces: %w", err)
	}

	owners := map[string]bool{}
	for i := range all.Items {
		owners[all.Items[i].Spec.Owner] = true
	}
	out := make([]model.UserUsage, 0, len(owners))
	for ownerID := range owners {
		row := model.UserUsage{UserID: ownerID}
		id := policy.Identity{Owner: ownerID}
		if user, err := s.users.FindByID(ctx, ownerID); err == nil {
			row.Username = user.Username
			row.Groups = user.Groups
			id = identityFor(user)
		}
		if pol, _, denial := policy.Resolve(policies.Items, id); denial == nil {
			row.Policy = pol.Name
		}
		use, err := s.usageOf(ctx, ownerID, images.Items)
		if err != nil {
			return nil, err
		}
		row.Workspaces = use.workspaces
		row.Used = use.used
		out = append(out, row)
	}
	return out, nil
}

// ------------------------------------------------------------- mapping

// imageToModel projects a WorkspaceImage plus its discovered catalog
// entries (now in Postgres, catalog_entries — see CatalogSyncWorker)
// into the API model. A method, not a free function, because the
// discovered entries require a DB round trip the caller's ctx must
// bound.
func (s *GovernanceService) imageToModel(ctx context.Context, img *waasv1alpha1.WorkspaceImage) (model.CatalogImage, error) {
	m := model.CatalogImage{
		Name:               img.Name,
		DisplayName:        img.Spec.DisplayName,
		Description:        img.Spec.Description,
		Image:              img.Spec.Image,
		Registry:           img.Spec.Registry,
		TagPolicy:          string(img.Spec.TagPolicy),
		Enabled:            img.Spec.Enabled,
		Architectures:      img.Spec.Architectures,
		AllowedGroups:      img.Spec.AllowedGroups,
		ImagePullSecretRef: img.Spec.ImagePullSecretRef,
	}
	for _, p := range img.Spec.Protocols {
		m.Protocols = append(m.Protocols, string(p))
	}
	if r := img.Spec.Resources; r != nil {
		m.Defaults = sizeToMap(r.Default)
		m.Min = sizeToMap(r.Min)
		m.Max = sizeToMap(r.Max)
	}
	if img.Spec.Catalog != nil {
		// Non-nil even before the first sync: presence of spec.catalog is
		// what gates the admin "Sync now" action.
		m.Catalog = &model.CatalogSyncStatus{}
		if st := img.Status.Catalog; st != nil {
			m.Catalog.Source = st.Source
			m.Catalog.LastSyncError = st.LastSyncError
			if st.LastSyncTime != nil {
				m.Catalog.LastSyncTime = &st.LastSyncTime.Time
			}
		}
	}
	entries, err := s.catalog.ListEntries(ctx, img.Name)
	if err != nil {
		return model.CatalogImage{}, fmt.Errorf("listing catalog entries of %s: %w", img.Name, err)
	}
	for _, e := range entries {
		m.Discovered = append(m.Discovered, model.DiscoveredImage{
			Image:         e.Image,
			OS:            e.OS,
			App:           e.App,
			Version:       e.Version,
			Icon:          e.Icon,
			DisplayName:   e.DisplayName,
			Description:   e.Description,
			Profile:       e.Profile,
			Recommended:   unmarshalRecommended(e.Recommended),
			Architectures: e.Architectures,
		})
	}
	return m, nil
}

// unmarshalRecommended decodes the opaque JSON persisted by
// CatalogSyncWorker; a nil/malformed column yields nil rather than an
// error — this is display/prefill data, never a reason to fail an API
// response.
func unmarshalRecommended(raw json.RawMessage) *model.DeploymentRecommendation {
	if len(raw) == 0 {
		return nil
	}
	var r model.DeploymentRecommendation
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil
	}
	return &r
}

func policyToModel(pol *waasv1alpha1.WorkspacePolicy) model.PolicyModel {
	m := model.PolicyModel{
		Name:     pol.Name,
		Priority: pol.Spec.Priority,
		Images:   pol.Spec.Images,
		Limits: model.PolicyLimitsModel{
			MaxWorkspaces:        pol.Spec.Limits.MaxWorkspaces,
			MaxRunningWorkspaces: pol.Spec.Limits.MaxRunningWorkspaces,
		},
	}
	for _, sub := range pol.Spec.Subjects {
		m.Subjects = append(m.Subjects, model.PolicySubject{Kind: string(sub.Kind), Name: sub.Name})
	}
	if pw := pol.Spec.Limits.PerWorkspace; pw != nil {
		m.Limits.PerWorkspace = map[string]string{}
		if pw.CPU != nil {
			m.Limits.PerWorkspace["cpu"] = pw.CPU.String()
		}
		if pw.Memory != nil {
			m.Limits.PerWorkspace["memory"] = pw.Memory.String()
		}
		if pw.Home != nil {
			m.Limits.PerWorkspace["home"] = pw.Home.String()
		}
	}
	if agg := pol.Spec.Limits.Aggregate; agg != nil {
		m.Limits.Aggregate = map[string]string{}
		if agg.CPU != nil {
			m.Limits.Aggregate["cpu"] = agg.CPU.String()
		}
		if agg.Memory != nil {
			m.Limits.Aggregate["memory"] = agg.Memory.String()
		}
		if agg.Storage != nil {
			m.Limits.Aggregate["storage"] = agg.Storage.String()
		}
	}
	m.Limits.Defaults = sizeToMap(pol.Spec.Limits.Defaults)
	if c := pol.Spec.Clipboard; c != nil {
		m.Clipboard = &model.ClipboardPolicyModel{
			CopyFromWorkspace: c.CopyFromWorkspace,
			PasteToWorkspace:  c.PasteToWorkspace,
		}
	}
	if lc := pol.Spec.Lifecycle; lc != nil {
		m.Lifecycle = map[string]string{}
		if lc.IdleSuspendAfter != nil {
			m.Lifecycle["idleSuspendAfter"] = lc.IdleSuspendAfter.Duration.String()
		}
		if lc.MaxLifetime != nil {
			m.Lifecycle["maxLifetime"] = lc.MaxLifetime.Duration.String()
		}
	}
	if ov := pol.Spec.Overrides; ov != nil {
		m.Overrides = &model.PolicyOverridesModel{AllowedFields: []string{}}
		for _, f := range ov.AllowedFields {
			m.Overrides.AllowedFields = append(m.Overrides.AllowedFields, string(f))
		}
	}
	m.RemoteWorkspaces = pol.Spec.RemoteWorkspaces
	return m
}

func sizeToMap(cs *waasv1alpha1.ComputeSize) map[string]string {
	if cs == nil {
		return nil
	}
	out := map[string]string{}
	if cs.CPU != nil {
		out["cpu"] = cs.CPU.String()
	}
	if cs.Memory != nil {
		out["memory"] = cs.Memory.String()
	}
	return out
}

func computeSizes(defaults, minM, maxM map[string]string) (*waasv1alpha1.ImageResources, error) {
	d, err := parseSize(defaults, "defaults")
	if err != nil {
		return nil, err
	}
	mn, err := parseSize(minM, "min")
	if err != nil {
		return nil, err
	}
	mx, err := parseSize(maxM, "max")
	if err != nil {
		return nil, err
	}
	if d == nil && mn == nil && mx == nil {
		return nil, nil
	}
	return &waasv1alpha1.ImageResources{Default: d, Min: mn, Max: mx}, nil
}

func parseSize(m map[string]string, field string) (*waasv1alpha1.ComputeSize, error) {
	if len(m) == 0 {
		return nil, nil
	}
	cs := &waasv1alpha1.ComputeSize{}
	for k, v := range m {
		q, err := parseQty(v)
		if err != nil {
			return nil, apierror.BadRequest(fmt.Sprintf("%s.%s: invalid quantity %q", field, k, v))
		}
		switch strings.ToLower(k) {
		case "cpu":
			cs.CPU = q
		case "memory":
			cs.Memory = q
		default:
			return nil, apierror.BadRequest(fmt.Sprintf("%s: unknown key %q (cpu/memory)", field, k))
		}
	}
	return cs, nil
}
