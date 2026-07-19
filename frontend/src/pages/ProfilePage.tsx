import { useState, type FormEvent, type ReactNode } from 'react';
import { useNavigate } from 'react-router';
import { useTranslation } from 'react-i18next';
import { Avatar } from '@/components/Avatar';
import { useUpdateProfile } from '@/hooks/useApi';
import { applyTheme } from '@/lib/theme';
import { useAuthStore } from '@/stores/authStore';
import type { Theme } from '@/types';

// Self-service profile: identity (until OIDC owns it), UI preferences and
// password. Each card saves independently.
export function ProfilePage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const user = useAuthStore((s) => s.user);

  if (!user) return null;

  return (
    <div className="app-background min-h-screen">
      <header className="flex items-center gap-4 bg-white px-6 py-4 shadow-sm dark:bg-slate-800">
        <button
          onClick={() => navigate('/')}
          className="text-sm text-blue-600 hover:underline dark:text-blue-400"
        >
          ← {t('profile.backToPortal')}
        </button>
        <h1 className="text-lg font-semibold text-slate-900 dark:text-white">
          {t('profile.title')}
        </h1>
      </header>

      <main className="mx-auto max-w-2xl space-y-6 p-6">
        <IdentityCard />
        <PreferencesCard />
        <PasswordCard />
      </main>
    </div>
  );
}

function Card({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="space-y-4 rounded-xl bg-white p-6 shadow-sm dark:bg-slate-800">
      <h2 className="font-medium text-slate-900 dark:text-white">{title}</h2>
      {children}
    </section>
  );
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label className="block">
      <span className="text-sm text-slate-600 dark:text-slate-300">{label}</span>
      {children}
    </label>
  );
}

const inputClass =
  'mt-1 w-full rounded-md border border-slate-300 px-3 py-2 disabled:cursor-not-allowed disabled:opacity-50 dark:border-slate-600 dark:bg-slate-700 dark:text-white';

function SaveButton({ pending, disabled }: { pending: boolean; disabled?: boolean }) {
  const { t } = useTranslation();
  return (
    <button
      type="submit"
      disabled={pending || disabled}
      className="rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"
    >
      {t('app.save')}
    </button>
  );
}

function Feedback({ error, saved }: { error?: string; saved: boolean }) {
  const { t } = useTranslation();
  if (error) return <p className="text-sm text-red-600">{error}</p>;
  if (saved) return <p className="text-sm text-emerald-600">{t('profile.saved')}</p>;
  return null;
}

function IdentityCard() {
  const { t } = useTranslation();
  const user = useAuthStore((s) => s.user)!;
  const sso = user.sso;
  const update = useUpdateProfile();
  const [displayName, setDisplayName] = useState(user.displayName ?? '');
  const [email, setEmail] = useState(user.email ?? '');
  const [saved, setSaved] = useState(false);

  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    if (sso) return;
    setSaved(false);
    update.mutate({ displayName, email }, { onSuccess: () => setSaved(true) });
  };

  return (
    <Card title={t('profile.identity')}>
      <div className="flex items-center gap-4">
        <Avatar user={{ username: user.username, displayName }} size="lg" />
        <div>
          <p className="font-medium text-slate-900 dark:text-white">
            {displayName || user.username}
          </p>
          <p className="text-sm text-slate-500 dark:text-slate-400">
            {user.username}
            <span className="ml-2 rounded bg-slate-100 px-1.5 py-0.5 text-xs text-slate-500 dark:bg-slate-700 dark:text-slate-400">
              {t('profile.loginId')}
            </span>
          </p>
        </div>
      </div>
      <form onSubmit={onSubmit} className="space-y-4">
        <Field label={t('profile.displayName')}>
          <input
            className={inputClass}
            value={displayName}
            onChange={(e) => setDisplayName(e.target.value)}
            placeholder={user.username}
            disabled={sso}
            title={sso ? t('profile.ssoManaged') : undefined}
          />
        </Field>
        <Field label={t('profile.email')}>
          <input
            type="email"
            className={inputClass}
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            disabled={sso}
            title={sso ? t('profile.ssoManaged') : undefined}
          />
        </Field>
        <p className="text-xs text-slate-400 dark:text-slate-500">
          {sso ? t('profile.ssoManaged') : t('profile.oidcNote')}
        </p>
        <Feedback error={update.isError ? update.error.message : undefined} saved={saved} />
        <SaveButton pending={update.isPending} disabled={sso} />
      </form>
    </Card>
  );
}

function PreferencesCard() {
  const { t, i18n } = useTranslation();
  const user = useAuthStore((s) => s.user)!;
  const update = useUpdateProfile();
  const [saved, setSaved] = useState(false);

  const save = (prefs: Partial<NonNullable<typeof user.preferences>>) => {
    setSaved(false);
    update.mutate(
      { preferences: { ...user.preferences, ...prefs } },
      { onSuccess: () => setSaved(true) },
    );
  };

  const openInNewTab = user.preferences?.openWorkspaceInNewTab;

  return (
    <Card title={t('profile.preferences')}>
      <fieldset>
        <legend className="text-sm text-slate-600 dark:text-slate-300">
          {t('profile.openTarget')}
        </legend>
        <div className="mt-2 space-y-2">
          <label className="flex items-center gap-2 text-sm text-slate-700 dark:text-slate-200">
            <input
              type="radio"
              name="openTarget"
              checked={openInNewTab === false}
              onChange={() => save({ openWorkspaceInNewTab: false })}
            />
            {t('portal.openSameTab')}
          </label>
          <label className="flex items-center gap-2 text-sm text-slate-700 dark:text-slate-200">
            <input
              type="radio"
              name="openTarget"
              checked={openInNewTab === true}
              onChange={() => save({ openWorkspaceInNewTab: true })}
            />
            {t('portal.openNewTab')}
          </label>
          {openInNewTab == null && (
            <p className="text-xs text-slate-400 dark:text-slate-500">
              {t('profile.openTargetUnset')}
            </p>
          )}
        </div>
      </fieldset>
      <Field label={t('profile.theme')}>
        <select
          className={inputClass}
          value={user.preferences?.theme || 'system'}
          onChange={(e) => {
            const value = e.target.value as Theme;
            applyTheme(value);
            save({ theme: value === 'system' ? '' : value });
          }}
        >
          <option value="system">{t('profile.themeSystem')}</option>
          <option value="light">{t('profile.themeLight')}</option>
          <option value="dark">{t('profile.themeDark')}</option>
        </select>
      </Field>
      <Field label={t('profile.language')}>
        <select
          className={inputClass}
          value={user.preferences?.language || i18n.language.split('-')[0]}
          onChange={(e) => {
            void i18n.changeLanguage(e.target.value);
            save({ language: e.target.value });
          }}
        >
          <option value="en">English</option>
          <option value="fr">Français</option>
        </select>
      </Field>
      <Feedback error={update.isError ? update.error.message : undefined} saved={saved} />
    </Card>
  );
}

function PasswordCard() {
  const { t } = useTranslation();
  const user = useAuthStore((s) => s.user)!;
  const sso = user.sso;
  const update = useUpdateProfile();
  const [current, setCurrent] = useState('');
  const [next, setNext] = useState('');
  const [confirm, setConfirm] = useState('');
  const [saved, setSaved] = useState(false);
  const mismatch = confirm !== '' && next !== confirm;

  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    if (sso || mismatch || !next) return;
    setSaved(false);
    update.mutate(
      { currentPassword: current, newPassword: next },
      {
        onSuccess: () => {
          setSaved(true);
          setCurrent('');
          setNext('');
          setConfirm('');
        },
      },
    );
  };

  return (
    <Card title={t('profile.security')}>
      {sso && (
        <p className="text-sm text-slate-500 dark:text-slate-400">{t('profile.ssoPassword')}</p>
      )}
      <form onSubmit={onSubmit} className="space-y-4">
        <fieldset
          disabled={sso}
          className={sso ? 'space-y-4 opacity-50' : 'space-y-4'}
          title={sso ? t('profile.ssoPassword') : undefined}
        >
          <Field label={t('profile.currentPassword')}>
            <input
              type="password"
              className={inputClass}
              value={current}
              onChange={(e) => setCurrent(e.target.value)}
              autoComplete="current-password"
              required
            />
          </Field>
          <Field label={t('profile.newPassword')}>
            <input
              type="password"
              className={inputClass}
              value={next}
              onChange={(e) => setNext(e.target.value)}
              autoComplete="new-password"
              required
              minLength={8}
            />
          </Field>
          <Field label={t('profile.confirmPassword')}>
            <input
              type="password"
              className={inputClass}
              value={confirm}
              onChange={(e) => setConfirm(e.target.value)}
              autoComplete="new-password"
              required
            />
          </Field>
          {mismatch && <p className="text-sm text-red-600">{t('profile.passwordMismatch')}</p>}
          <Feedback error={update.isError ? update.error.message : undefined} saved={saved} />
          <SaveButton pending={update.isPending} />
        </fieldset>
      </form>
    </Card>
  );
}
