// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@/test/render';
import { createApiMock } from '@/test/apiMock';
import en from '@/i18n/locales/en.json';
import type { CatalogImage } from '@/types';
import type { TemplateInput } from '@/hooks/useApi';
import { TemplateDialog } from './TemplateDialog';

const apiMock = createApiMock();
vi.mock('@/lib/api', () => ({
  get api() {
    return apiMock.api;
  },
}));

const catalogs: CatalogImage[] = [
  {
    name: 'browsers',
    displayName: 'Browsers',
    registry: 'ghcr.io/acme/',
    enabled: true,
    discovered: [
      {
        image: 'ghcr.io/acme/firefox:128',
        app: 'firefox',
        displayName: 'Firefox',
        version: '128',
        os: 'linux',
        profile: 'hardened',
        recommended: {
          podSecurityContext: { runAsUser: 1000 },
          securityContext: { readOnlyRootFilesystem: true },
          volumes: [{ name: 'tmp', mountPath: '/tmp' }],
          env: [{ name: 'WAAS_SSH_ENABLED', default: '0' }],
        },
      },
    ],
  },
];

const initial: TemplateInput = {
  name: 'my-template',
  displayName: 'My Template',
  os: 'linux',
  image: '',
};

beforeEach(() => {
  apiMock.route('/api/v1/admin/images', catalogs);
  apiMock.route('/api/v1/meta/placeholders', []);
  apiMock.route('/api/v1/meta/override-fields', []);
  apiMock.route('/api/v1/meta/protocols', []);
});

describe('TemplateDialog — apply catalog recommendation', () => {
  it('prefills the workload YAML/env and expands the collapsed Workload section', async () => {
    renderWithProviders(<TemplateDialog isNew initial={initial} onClose={() => {}} />);

    // Workload starts collapsed (native <details>, no open prop).
    const details = document.querySelector('details');
    expect(details?.open).toBe(false);

    await userEvent.click(
      screen.getByRole('button', { name: en.admin.templatesPage.imageCatalog }),
    );
    await userEvent.click(await screen.findByRole('option', { name: /Browsers/ }));
    await userEvent.click(await screen.findByRole('option', { name: /Firefox/ }));
    await userEvent.click(
      screen.getByRole('button', { name: en.admin.templatesPage.applyRecommendation }),
    );

    expect(details?.open).toBe(true);
    const workloadYaml = (details?.querySelector('textarea') as HTMLTextAreaElement).value;
    expect(workloadYaml).toContain('runAsUser: 1000');
    expect(workloadYaml).toContain('readOnlyRootFilesystem: true');
    expect(workloadYaml).toContain('mountPath: /tmp');
    // env goes to EnvFieldset's input.env, not the workload YAML.
    expect(workloadYaml).not.toContain('WAAS_SSH_ENABLED');
    expect(screen.getByDisplayValue('WAAS_SSH_ENABLED')).toBeInTheDocument();
  });
});
