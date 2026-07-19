import type { RemoteWorkspace, Workspace } from '@/types';

/**
 * SessionTarget is THE convergence point between in-cluster and remote
 * workspaces: every shared surface (card, session overlay, protocol
 * switch, connect flow) consumes this shape and never branches on the
 * kind — kind-specific behavior is declared once here as capabilities.
 *
 * Adding a card/overlay feature = write it once against SessionTarget;
 * adding a kind-specific action = add a capability flag here and gate
 * the single implementation with it. See docs/frontend-capabilities.md.
 */

export interface TargetProtocol {
  name: string;
  port?: number;
  default?: boolean;
  /** Locked params (template/registered values) — placeholders in forms. */
  params?: Record<string, string>;
  /** Flat connect-time allow-list for non-admins, resolved server-side
   * (the template's userParams with cat: selectors already expanded);
   * undefined = every non-platform param (remote machines). */
  resolvedUserParams?: string[];
}

export interface TargetCapabilities {
  /** Lifecycle pause/resume (operator-managed compute). */
  pause: boolean;
  /** Wake-on-LAN (registered MAC). */
  wake: boolean;
  /** Open in the split view (in-cluster panes only today). */
  splitView: boolean;
  /** Per-workspace connection settings dialog (profile preferences). */
  connectionSettings: boolean;
  /** Endpoint/credentials edit dialog (user-owned machines). */
  editEndpoint: boolean;
  /** Has an operator-reported lifecycle phase. */
  hasPhase: boolean;
  /** Manual reload of a pending configuration change (drift badge click):
   *  operator-managed compute only — remote machines have no template to
   *  drift from. */
  reload: boolean;
}

export interface SessionTarget {
  id: string;
  kind: 'workspace' | 'remote';
  displayName: string;
  /** Secondary line on the card: template for in-cluster, host for remote. */
  subtitle: string;
  /** The backing template's description — the card's "?" tooltip next
   *  to the subtitle. Workspace kind only; remote machines have none. */
  description?: string;
  connectUrl: string;
  protocols: TargetProtocol[];
  defaultProtocol: string;
  capabilities: TargetCapabilities;
  /** Pending template update: the workspace restarts with it at the
   *  next resume. Workspace kind only. */
  templateDrifted?: boolean;
  /** OS family ("linux"/"windows") for the card logo; absent (remote
   *  machines) renders the linux fallback. */
  os?: string;
  /** Icon reference for the card logo (template spec.logo or catalog
   *  icon, resolved by the caller via templateIcon); absent = the OS
   *  fallback. */
  icon?: string;
}

export function targetFromWorkspace(
  ws: Workspace,
  icon?: string,
  description?: string,
): SessionTarget {
  const protocols: TargetProtocol[] = (ws.protocols ?? []).map((p) => ({
    name: p.name,
    port: p.port,
    default: p.default,
    params: p.params,
    resolvedUserParams: p.resolvedUserParams,
  }));
  return {
    id: ws.id,
    kind: 'workspace',
    displayName: ws.displayName || ws.name,
    subtitle: ws.templateRef,
    description,
    connectUrl: `/workspaces/${ws.id}/connect`,
    protocols,
    defaultProtocol: protocols.find((p) => p.default)?.name ?? ws.protocol ?? '',
    templateDrifted: ws.templateDrifted,
    os: ws.os,
    icon,
    capabilities: {
      pause: true,
      wake: false,
      splitView: true,
      connectionSettings: true,
      editEndpoint: false,
      hasPhase: true,
      reload: true,
    },
  };
}

export function targetFromRemote(rw: RemoteWorkspace): SessionTarget {
  // Legacy rows predate the protocols list: synthesize the single entry
  // (same rule as the server's EffectiveProtocols).
  const protocols: TargetProtocol[] =
    rw.protocols && rw.protocols.length > 0
      ? rw.protocols
      : [{ name: rw.protocol, port: rw.port, default: true, params: rw.params }];
  return {
    id: rw.id,
    kind: 'remote',
    displayName: rw.name,
    subtitle: `${rw.hostname}`,
    connectUrl: `/remote/${rw.id}/connect`,
    protocols,
    defaultProtocol: protocols.find((p) => p.default)?.name ?? protocols[0]?.name ?? '',
    capabilities: {
      pause: false,
      wake: Boolean(rw.macAddress),
      splitView: false,
      connectionSettings: false,
      editEndpoint: true,
      hasPhase: false,
      reload: false,
    },
  };
}
