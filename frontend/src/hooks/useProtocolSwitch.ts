import { useTranslation } from 'react-i18next';
import { useUpdateProfile } from '@/hooks/useApi';
import { useAuthStore } from '@/stores/authStore';
import type { SessionTarget } from '@/lib/target';

/**
 * THE protocol-switch logic, shared by the card chips and the in-session
 * overlay for BOTH kinds: the chosen protocol is a profile preference
 * (workspaceSettings[target.id].protocol) that the connect flow sends and
 * the server validates against what the target actually declares — the
 * client never widens anything.
 *
 * Switching keeps per-protocol params (paramsByProtocol) so flipping back
 * restores earlier tuning. With `confirm`, an open session asks before
 * reconnecting (changing the preference re-runs DesktopPane's connection
 * effect — that IS the reconnect).
 */
export function useProtocolSwitch(target: SessionTarget, opts?: { confirm?: boolean }) {
  const { t } = useTranslation();
  const user = useAuthStore((s) => s.user);
  const updateProfile = useUpdateProfile();

  const saved = user?.preferences?.workspaceSettings?.[target.id];
  const active = saved?.protocol ?? target.defaultProtocol;

  const switchTo = (next: string) => {
    if (next === active || updateProfile.isPending) return;
    if (
      opts?.confirm &&
      !window.confirm(t('overlay.switchConfirm', { protocol: next.toUpperCase() }))
    ) {
      return;
    }
    const byProto = { ...saved?.paramsByProtocol };
    if (saved?.params && active) byProto[active] = saved.params;
    const settings = { ...user?.preferences?.workspaceSettings };
    settings[target.id] = {
      protocol: next !== target.defaultProtocol ? next : undefined,
      params: byProto[next],
      paramsByProtocol: Object.keys(byProto).length > 0 ? byProto : undefined,
    };
    updateProfile.mutate({ preferences: { ...user?.preferences, workspaceSettings: settings } });
  };

  return { active, switchTo, pending: updateProfile.isPending };
}
