// Hand-curated shapes referenced by the GENERATED types (types.gen.ts):
// Kubernetes passthrough types (stable upstream forms the Go models
// embed) and the auth role. Everything else API-shaped lives in
// types.gen.ts — do not add app models here.

export type Role = 'admin' | 'user';

/** k8s corev1.EnvVar passthrough (value or secret/configmap reference). */
export interface EnvVar {
  name: string;
  value?: string;
  valueFrom?: {
    secretKeyRef?: { name: string; key: string };
    configMapKeyRef?: { name: string; key: string };
  };
}

/** k8s corev1.Toleration passthrough (operator/effect are upstream
 *  enums: Exists/Equal, NoSchedule/PreferNoSchedule/NoExecute). */
export interface Toleration {
  key?: string;
  operator?: string;
  value?: string;
  effect?: string;
  tolerationSeconds?: number;
}

export interface WorkspaceSchedule {
  timezone?: string;
  uptime?: string[];
  downtime?: string[];
}

/** Template workload passthrough (kind + pod-spec fragments), kept
 *  loose on purpose: the operator/webhook validate, the UI displays. */
export interface WorkspaceWorkload {
  kind?: string;
  labels?: Record<string, string>;
  annotations?: Record<string, string>;
  serviceAccountName?: string;
  nodeSelector?: Record<string, string>;
  tolerations?: unknown[];
  securityContext?: unknown;
  podSecurityContext?: unknown;
  volumes?: unknown[];
  volumeMounts?: unknown[];
}

export interface WorkspacePlacement {
  namespace?: string;
  namespaceLabels?: Record<string, string>;
  namespaceAnnotations?: Record<string, string>;
  cleanup?: string;
}
