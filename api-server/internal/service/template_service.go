package service

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"

	"github.com/xorhub/waas/api-server/internal/apierror"
	"github.com/xorhub/waas/api-server/internal/model"
	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
	"github.com/xorhub/waas/operator/pkg/params"
)

// TemplateService manages WorkspaceTemplate CRs. Templates live in the
// cluster only (GitOps-first): kubectl-applied and API-created templates are
// the same objects.
type TemplateService struct {
	kube      client.Client
	namespace string
	audit     *AuditService
}

func NewTemplateService(kube client.Client, namespace string, audit *AuditService) *TemplateService {
	return &TemplateService{kube: kube, namespace: namespace, audit: audit}
}

// TemplateInput is the create/update payload. It deliberately reuses the
// CR types (corev1.EnvVar, WorkspaceWorkload) for the pod-spec facets:
// the API accepts exactly what the CR schema accepts, so new CR fields
// never need a parallel DTO — the "no duplicated schema" decision.
type TemplateInput struct {
	Name             string            `json:"name"`
	DisplayName      string            `json:"displayName"`
	Description      string            `json:"description"`
	OS               string            `json:"os"`
	Image            string            `json:"image"`
	Port             int32             `json:"port"`
	HomeSize         string            `json:"homeSize"`
	StorageClassName string            `json:"storageClassName"`
	Requests         map[string]string `json:"requests"`
	Limits           map[string]string `json:"limits"`

	// Env is the CR field verbatim — valueFrom/secretKeyRef included, so
	// credentials can reference Secrets instead of carrying literals.
	Env []corev1.EnvVar `json:"env,omitempty"`
	// Workload is the CR field verbatim (kind, security contexts,
	// volumes, nodeSelector, tolerations, serviceAccountName).
	Workload *waasv1alpha1.WorkspaceWorkload `json:"workload,omitempty"`

	Protocols []TemplateProtocolInput `json:"protocols,omitempty"`
	Overrides *TemplateOverridesInput `json:"overrides,omitempty"`
}

// TemplateProtocolInput mirrors WorkspaceProtocol.
type TemplateProtocolInput struct {
	Name                 string            `json:"name"`
	Port                 int32             `json:"port"`
	Default              bool              `json:"default"`
	Params               map[string]string `json:"params,omitempty"`
	UserParams           []string          `json:"userParams,omitempty"`
	CredentialsSecretRef string            `json:"credentialsSecretRef,omitempty"`
}

// TemplateOverridesInput mirrors TemplateOverrides.
type TemplateOverridesInput struct {
	AllowedFields []string `json:"allowedFields,omitempty"`
	Owner         string   `json:"owner,omitempty"`
}

func (s *TemplateService) List(ctx context.Context) ([]model.WorkspaceTemplate, error) {
	list := &waasv1alpha1.WorkspaceTemplateList{}
	if err := s.kube.List(ctx, list, client.InNamespace(s.namespace)); err != nil {
		return nil, fmt.Errorf("listing workspace templates: %w", err)
	}
	out := make([]model.WorkspaceTemplate, 0, len(list.Items))
	for i := range list.Items {
		out = append(out, templateToModel(&list.Items[i]))
	}
	return out, nil
}

func (s *TemplateService) Get(ctx context.Context, name string) (*model.WorkspaceTemplate, error) {
	tpl, err := s.fetch(ctx, name)
	if err != nil {
		return nil, err
	}
	m := templateToModel(tpl)
	return &m, nil
}

func (s *TemplateService) Create(ctx context.Context, actor Actor, in TemplateInput) (*model.WorkspaceTemplate, error) {
	if errs := validation.IsDNS1123Subdomain(in.Name); in.Name == "" || len(errs) > 0 {
		return nil, apierror.BadRequest("name must be a valid DNS-1123 subdomain")
	}
	spec, err := specFromInput(in)
	if err != nil {
		return nil, err
	}
	tpl := &waasv1alpha1.WorkspaceTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: in.Name, Namespace: s.namespace},
		Spec:       *spec,
	}
	if err := s.kube.Create(ctx, tpl); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return nil, apierror.Conflict(fmt.Sprintf("template %q already exists", in.Name))
		}
		return nil, fmt.Errorf("creating template %s: %w", in.Name, err)
	}
	s.audit.Record(ctx, actor, "workspace_template.created", "workspace_template", in.Name, "")
	m := templateToModel(tpl)
	return &m, nil
}

func (s *TemplateService) Update(ctx context.Context, actor Actor, name string, in TemplateInput) (*model.WorkspaceTemplate, error) {
	tpl, err := s.fetch(ctx, name)
	if err != nil {
		return nil, err
	}
	in.Name = name
	spec, err := specFromInput(in)
	if err != nil {
		return nil, err
	}
	tpl.Spec = *spec
	if err := s.kube.Update(ctx, tpl); err != nil {
		return nil, fmt.Errorf("updating template %s: %w", name, err)
	}
	s.audit.Record(ctx, actor, "workspace_template.updated", "workspace_template", name, "")
	m := templateToModel(tpl)
	return &m, nil
}

func (s *TemplateService) Delete(ctx context.Context, actor Actor, name string) error {
	tpl, err := s.fetch(ctx, name)
	if err != nil {
		return err
	}

	// Refuse to delete a template still referenced by workspaces.
	workspaces := &waasv1alpha1.WorkspaceList{}
	if err := s.kube.List(ctx, workspaces, client.InNamespace(s.namespace)); err != nil {
		return fmt.Errorf("listing workspaces: %w", err)
	}
	for i := range workspaces.Items {
		if workspaces.Items[i].Spec.TemplateRef == name {
			return apierror.Conflict(fmt.Sprintf("template %q is used by workspace %q", name, workspaces.Items[i].Name))
		}
	}

	if err := s.kube.Delete(ctx, tpl); err != nil {
		return fmt.Errorf("deleting template %s: %w", name, err)
	}
	s.audit.Record(ctx, actor, "workspace_template.deleted", "workspace_template", name, "")
	return nil
}

func (s *TemplateService) fetch(ctx context.Context, name string) (*waasv1alpha1.WorkspaceTemplate, error) {
	tpl := &waasv1alpha1.WorkspaceTemplate{}
	err := s.kube.Get(ctx, types.NamespacedName{Namespace: s.namespace, Name: name}, tpl)
	if apierrors.IsNotFound(err) {
		return nil, apierror.NotFound(fmt.Sprintf("template %q not found", name))
	}
	if err != nil {
		return nil, fmt.Errorf("fetching template %s: %w", name, err)
	}
	return tpl, nil
}

func specFromInput(in TemplateInput) (*waasv1alpha1.WorkspaceTemplateSpec, error) {
	if in.DisplayName == "" || in.Image == "" {
		return nil, apierror.BadRequest("displayName and image are required")
	}
	os := waasv1alpha1.OSType(in.OS)
	if os != waasv1alpha1.OSLinux && os != waasv1alpha1.OSWindows {
		return nil, apierror.BadRequest("os must be linux or windows")
	}
	spec := &waasv1alpha1.WorkspaceTemplateSpec{
		DisplayName: in.DisplayName,
		Description: in.Description,
		OS:          os,
		Image:       in.Image,
		Port:        in.Port,
		Env:         in.Env,
		Workload:    in.Workload,
	}
	if in.HomeSize != "" {
		qty, err := resource.ParseQuantity(in.HomeSize)
		if err != nil {
			return nil, apierror.BadRequest("homeSize must be a valid quantity (e.g. 10Gi)")
		}
		spec.HomeSize = &qty
	}
	if in.StorageClassName != "" {
		sc := in.StorageClassName
		spec.StorageClassName = &sc
	}
	requests, err := resourceList(in.Requests)
	if err != nil {
		return nil, err
	}
	limits, err := resourceList(in.Limits)
	if err != nil {
		return nil, err
	}
	spec.Resources = corev1.ResourceRequirements{Requests: requests, Limits: limits}

	// Protocols: same registry gate as the admission webhook, but with
	// 400s and field-level messages instead of a denied kubectl apply.
	defaults := 0
	for _, p := range in.Protocols {
		if p.Name == "" || p.Port <= 0 {
			return nil, apierror.BadRequest("each protocol needs a name and a port")
		}
		if p.Default {
			defaults++
		}
		if v := params.ValidateTemplateParams(p.Name, p.Params); v != nil {
			return nil, apierror.BadRequest(fmt.Sprintf("protocols[%s].params: %v", p.Name, v))
		}
		if v := params.ValidateUserParamNames(p.Name, p.UserParams); v != nil {
			return nil, apierror.BadRequest(fmt.Sprintf("protocols[%s].userParams: %v", p.Name, v))
		}
		spec.Protocols = append(spec.Protocols, waasv1alpha1.WorkspaceProtocol{
			Name:                 p.Name,
			Port:                 p.Port,
			Default:              p.Default,
			Params:               p.Params,
			UserParams:           p.UserParams,
			CredentialsSecretRef: p.CredentialsSecretRef,
		})
	}
	if defaults > 1 {
		return nil, apierror.BadRequest("at most one protocol may be marked default")
	}

	if in.Overrides != nil {
		ov := &waasv1alpha1.TemplateOverrides{Owner: in.Overrides.Owner}
		for _, f := range in.Overrides.AllowedFields {
			field := waasv1alpha1.OverridableField(f)
			switch field {
			case waasv1alpha1.FieldEnv, waasv1alpha1.FieldSecurityContext, waasv1alpha1.FieldPodSecurityContext,
				waasv1alpha1.FieldVolumes, waasv1alpha1.FieldNodeSelector, waasv1alpha1.FieldTolerations,
				waasv1alpha1.FieldResources, waasv1alpha1.FieldProtocol, waasv1alpha1.FieldProtocolParams:
				ov.AllowedFields = append(ov.AllowedFields, field)
			default:
				return nil, apierror.BadRequest(fmt.Sprintf("unknown overridable field %q", f))
			}
		}
		spec.Overrides = ov
	}
	return spec, nil
}

func resourceList(in map[string]string) (corev1.ResourceList, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := corev1.ResourceList{}
	for name, value := range in {
		qty, err := resource.ParseQuantity(value)
		if err != nil {
			return nil, apierror.BadRequest(fmt.Sprintf("invalid quantity %q for %s", value, name))
		}
		out[corev1.ResourceName(name)] = qty
	}
	return out, nil
}

func templateToModel(tpl *waasv1alpha1.WorkspaceTemplate) model.WorkspaceTemplate {
	m := model.WorkspaceTemplate{
		ID:          string(tpl.UID),
		Name:        tpl.Name,
		DisplayName: tpl.Spec.DisplayName,
		Description: tpl.Spec.Description,
		OS:          string(tpl.Spec.OS),
		Image:       tpl.Spec.Image,
		Port:        tpl.Spec.DesktopPort(),
		CreatedAt:   tpl.CreationTimestamp.Time,
	}
	if tpl.Spec.HomeSize != nil {
		m.HomeSize = tpl.Spec.HomeSize.String()
	}
	if len(tpl.Spec.Resources.Requests) > 0 {
		m.Requests = map[string]string{}
		for name, qty := range tpl.Spec.Resources.Requests {
			m.Requests[string(name)] = qty.String()
		}
	}
	if len(tpl.Spec.Resources.Limits) > 0 {
		m.Limits = map[string]string{}
		for name, qty := range tpl.Spec.Resources.Limits {
			m.Limits[string(name)] = qty.String()
		}
	}
	m.Workload = string(tpl.Spec.WorkloadKindOrDefault())
	m.WorkloadSpec = tpl.Spec.Workload
	m.Env = tpl.Spec.Env
	if tpl.Spec.StorageClassName != nil {
		m.StorageClassName = *tpl.Spec.StorageClassName
	}
	def := tpl.Spec.DefaultProtocol()
	for _, p := range tpl.Spec.EffectiveProtocols() {
		m.Protocols = append(m.Protocols, model.WorkspaceProtocol{
			Name:                 p.Name,
			Port:                 p.Port,
			Default:              p.Name == def.Name,
			Params:               p.Params,
			UserParams:           p.UserParams,
			CredentialsSecretRef: p.CredentialsSecretRef,
		})
	}
	if tpl.Spec.Overrides != nil {
		for _, f := range tpl.Spec.Overrides.AllowedFields {
			m.AllowedOverrides = append(m.AllowedOverrides, string(f))
		}
		m.OverridesOwner = tpl.Spec.Overrides.Owner
	}
	return m
}
