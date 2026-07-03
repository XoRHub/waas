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

// TemplateInput is the create/update payload.
type TemplateInput struct {
	Name        string            `json:"name"`
	DisplayName string            `json:"displayName"`
	Description string            `json:"description"`
	OS          string            `json:"os"`
	Image       string            `json:"image"`
	Port        int32             `json:"port"`
	HomeSize    string            `json:"homeSize"`
	Requests    map[string]string `json:"requests"`
	Limits      map[string]string `json:"limits"`
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
	}
	if in.HomeSize != "" {
		qty, err := resource.ParseQuantity(in.HomeSize)
		if err != nil {
			return nil, apierror.BadRequest("homeSize must be a valid quantity (e.g. 10Gi)")
		}
		spec.HomeSize = &qty
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
	return m
}
