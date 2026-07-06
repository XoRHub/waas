package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
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
}

// NewGovernanceService wires the governance projections.
func NewGovernanceService(kube client.Client, namespace string, users repository.UserRepository, audit *AuditService) *GovernanceService {
	return &GovernanceService{kube: kube, namespace: namespace, users: users, audit: audit}
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
	templatesByImage := map[string][]string{}
	for i := range templates.Items {
		t := &templates.Items[i]
		templatesByImage[t.Spec.Image] = append(templatesByImage[t.Spec.Image], t.Name)
	}

	allowed := policy.AllowedImages(images.Items, pol, id)
	out := make([]model.CatalogImage, 0, len(allowed))
	for i := range allowed {
		m := imageToModel(&allowed[i])
		m.Templates = templatesByImage[allowed[i].Spec.Image]
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
	if denial != nil {
		return &model.QuotaStatus{Policy: ""}, nil
	}

	count, used, err := s.usageOf(ctx, user.ID, images.Items)
	if err != nil {
		return nil, err
	}

	status := &model.QuotaStatus{
		Policy:         pol.Name,
		PolicyPriority: pol.Spec.Priority,
		MaxWorkspaces:  pol.Spec.Limits.MaxWorkspaces,
		UsedWorkspaces: count,
		Used:           used,
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

// usageOf computes one owner's live aggregates (same math as pkg/policy).
func (s *GovernanceService) usageOf(ctx context.Context, ownerID string, catalog []waasv1alpha1.WorkspaceImage) (int, map[string]string, error) {
	all := &waasv1alpha1.WorkspaceList{}
	if err := s.kube.List(ctx, all, client.InNamespace(s.namespace)); err != nil {
		return 0, nil, fmt.Errorf("listing workspaces: %w", err)
	}
	var loads []policy.Load
	for i := range all.Items {
		ws := &all.Items[i]
		if ws.Spec.Owner != ownerID || !ws.DeletionTimestamp.IsZero() {
			continue
		}
		tpl := &waasv1alpha1.WorkspaceTemplate{}
		err := s.kube.Get(ctx, client.ObjectKey{Namespace: s.namespace, Name: ws.Spec.TemplateRef}, tpl)
		if apierrors.IsNotFound(err) {
			loads = append(loads, policy.Load{Paused: ws.Spec.Paused})
			continue
		}
		if err != nil {
			return 0, nil, err
		}
		load, _ := policy.LoadOf(ws, tpl, policy.FindImage(catalog, tpl.Spec.Image))
		loads = append(loads, load)
	}

	var cpu, mem, sto = newQty(), newQty(), newQty()
	for _, l := range loads {
		if !l.Paused {
			cpu.Add(l.CPU)
			mem.Add(l.Memory)
		}
		sto.Add(l.Storage)
	}
	return len(loads), map[string]string{
		"cpu": cpu.String(), "memory": mem.String(), "storage": sto.String(),
	}, nil
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
		out = append(out, imageToModel(&images.Items[i]))
	}
	return out, nil
}

// UpsertImageInput is the admin payload for a catalog entry.
type UpsertImageInput struct {
	DisplayName   string            `json:"displayName"`
	Description   string            `json:"description"`
	Image         string            `json:"image"`
	Protocols     []string          `json:"protocols"`
	Architectures []string          `json:"architectures"`
	Enabled       *bool             `json:"enabled"`
	AllowedGroups []string          `json:"allowedGroups"`
	Defaults      map[string]string `json:"defaults"`
	Min           map[string]string `json:"min"`
	Max           map[string]string `json:"max"`
}

// AdminUpsertImage creates or updates a WorkspaceImage.
func (s *GovernanceService) AdminUpsertImage(ctx context.Context, actor Actor, name string, in UpsertImageInput) (*model.CatalogImage, error) {
	if name == "" || in.Image == "" || in.DisplayName == "" || len(in.Protocols) == 0 {
		return nil, apierror.BadRequest("name, displayName, image and protocols are required")
	}
	spec := waasv1alpha1.WorkspaceImageSpec{
		DisplayName:   in.DisplayName,
		Description:   in.Description,
		Image:         in.Image,
		Enabled:       in.Enabled == nil || *in.Enabled,
		AllowedGroups: in.AllowedGroups,
		Architectures: in.Architectures,
	}
	for _, p := range in.Protocols {
		switch waasv1alpha1.Protocol(p) {
		case waasv1alpha1.ProtocolVNC, waasv1alpha1.ProtocolRDP, waasv1alpha1.ProtocolSSH:
			spec.Protocols = append(spec.Protocols, waasv1alpha1.Protocol(p))
		default:
			return nil, apierror.BadRequest(fmt.Sprintf("unknown protocol %q", p))
		}
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
	m := imageToModel(img)
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
	m := imageToModel(img)
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
	Priority  int32                       `json:"priority"`
	Subjects  []model.PolicySubject       `json:"subjects"`
	Images    []string                    `json:"images"`
	Limits    model.PolicyLimitsModel     `json:"limits"`
	Lifecycle map[string]string           `json:"lifecycle"`
	Clipboard *model.ClipboardPolicyModel `json:"clipboard"`
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
		Groups:   user.Groups,
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
		count, used, err := s.usageOf(ctx, ownerID, images.Items)
		if err != nil {
			return nil, err
		}
		row.Workspaces = count
		row.Used = used
		out = append(out, row)
	}
	return out, nil
}

// ------------------------------------------------------------- mapping

func imageToModel(img *waasv1alpha1.WorkspaceImage) model.CatalogImage {
	m := model.CatalogImage{
		Name:          img.Name,
		DisplayName:   img.Spec.DisplayName,
		Description:   img.Spec.Description,
		Image:         img.Spec.Image,
		Enabled:       img.Spec.Enabled,
		Architectures: img.Spec.Architectures,
		AllowedGroups: img.Spec.AllowedGroups,
	}
	for _, p := range img.Spec.Protocols {
		m.Protocols = append(m.Protocols, string(p))
	}
	if r := img.Spec.Resources; r != nil {
		m.Defaults = sizeToMap(r.Default)
		m.Min = sizeToMap(r.Min)
		m.Max = sizeToMap(r.Max)
	}
	return m
}

func policyToModel(pol *waasv1alpha1.WorkspacePolicy) model.PolicyModel {
	m := model.PolicyModel{
		Name:     pol.Name,
		Priority: pol.Spec.Priority,
		Images:   pol.Spec.Images,
		Limits:   model.PolicyLimitsModel{MaxWorkspaces: pol.Spec.Limits.MaxWorkspaces},
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
