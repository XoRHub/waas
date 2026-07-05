package service

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/xorhub/waas/api-server/internal/apierror"
)

// Small helpers around resource.Quantity for the governance payloads.

func newQty() resource.Quantity { return resource.Quantity{} }

func parseQty(s string) (*resource.Quantity, error) {
	q, err := resource.ParseQuantity(s)
	if err != nil {
		return nil, err
	}
	return &q, nil
}

// requirementsFrom turns the user-chosen {"cpu","memory"} sizing into pod
// requirements with requests == limits: pkg/policy counts limits first and
// identical values give the pod Guaranteed QoS.
func requirementsFrom(m map[string]string) (*corev1.ResourceRequirements, error) {
	if len(m) == 0 {
		return nil, nil
	}
	rl := corev1.ResourceList{}
	for k, v := range m {
		var name corev1.ResourceName
		switch k {
		case "cpu":
			name = corev1.ResourceCPU
		case "memory":
			name = corev1.ResourceMemory
		default:
			return nil, apierror.BadRequest(fmt.Sprintf("resources: unknown key %q (cpu/memory)", k))
		}
		q, err := parseQty(v)
		if err != nil {
			return nil, apierror.BadRequest(fmt.Sprintf("resources.%s: invalid quantity %q", k, v))
		}
		rl[name] = *q
	}
	return &corev1.ResourceRequirements{Requests: rl.DeepCopy(), Limits: rl}, nil
}

// parseCaps parses a {"cpu": "2", "memory": "4Gi", ...} map into
// quantities, keeping the original keys for the caller to place.
func parseCaps(m map[string]string, field string) (map[string]*resource.Quantity, error) {
	if len(m) == 0 {
		return nil, nil
	}
	out := map[string]*resource.Quantity{}
	for k, v := range m {
		q, err := parseQty(v)
		if err != nil {
			return nil, apierror.BadRequest(fmt.Sprintf("limits.%s.%s: invalid quantity %q", field, k, v))
		}
		out[k] = q
	}
	return out, nil
}
