// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, screen, within } from '@testing-library/react';
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
    // kasmvnc alongside guacd protocols: the prefill must drop it
    // (kasmvnc exclusivity) and rdp is deliberately NOT supported.
    protocols: ['vnc', 'ssh', 'kasmvnc'],
    discovered: [
      {
        image: 'ghcr.io/acme/firefox:128',
        app: 'firefox',
        displayName: 'Firefox',
        version: '128',
        os: 'linux',
        profile: 'hardened',
        architectures: ['amd64'],
        recommended: {
          podSecurityContext: { runAsUser: 1000 },
          securityContext: { readOnlyRootFilesystem: true },
          volumes: [{ name: 'tmp', mountPath: '/tmp' }],
          env: [
            { name: 'WAAS_SSH_ENABLED', default: '0', protocols: ['ssh'] },
            {
              name: 'WAAS_SSH_AUTHORIZED_KEYS_FILE',
              protocols: ['ssh'],
              description: 'Path to the authorized keys file',
            },
            // requires pulls VNC_USER in even though its own protocols
            // (kasmvnc) never match the applied set.
            { name: 'VNC_PW', protocols: ['vnc'], requires: ['VNC_USER'] },
            { name: 'VNC_USER', protocols: ['kasmvnc'] },
            { name: 'RDP_DOMAIN', protocols: ['rdp'] },
          ],
        },
      },
      {
        image: 'ghcr.io/acme/chromium:126',
        app: 'chromium',
        displayName: 'Chromium',
        version: '126',
        os: 'linux',
        architectures: ['amd64', 'arm64'],
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

/** Navigate the sectioned dialog: Workspace section, then one inner tab. */
const openWorkspaceTab = async (inner: string) => {
  await userEvent.click(screen.getByRole('button', { name: en.admin.templatesPage.tabWorkspace }));
  await userEvent.click(screen.getByRole('button', { name: inner }));
};

const openProtocolsTab = async () => {
  await userEvent.click(screen.getByRole('button', { name: en.admin.templatesPage.protocols }));
};

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
    await openWorkspaceTab(en.admin.templatesPage.env);
    expect(screen.getByDisplayValue('WAAS_SSH_ENABLED')).toBeInTheDocument();
    // A hint without a default never becomes a real row — it shows up
    // as a greyed suggestion instead (adoption tested separately).
    expect(screen.queryByDisplayValue('WAAS_SSH_AUTHORIZED_KEYS_FILE')).toBeNull();
    expect(
      screen.getByRole('button', { name: /WAAS_SSH_AUTHORIZED_KEYS_FILE/ }),
    ).toBeInTheDocument();
  });

  it('adds the image-supported protocols on a protocol-less template, dropping kasmvnc from a mixed list', async () => {
    renderWithProviders(<TemplateDialog isNew initial={initial} onClose={() => {}} />);

    await userEvent.click(
      screen.getByRole('button', { name: en.admin.templatesPage.imageCatalog }),
    );
    await userEvent.click(await screen.findByRole('option', { name: /Browsers/ }));
    await userEvent.click(await screen.findByRole('option', { name: /Firefox/ }));
    await userEvent.click(
      screen.getByRole('button', { name: en.admin.templatesPage.applyRecommendation }),
    );

    // vnc + ssh tabs appear (kasmvnc dropped by exclusivity), vnc is
    // active with its registry default port.
    await openProtocolsTab();
    expect(screen.getByRole('button', { name: /vnc/ })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /ssh/ })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /kasmvnc/ })).toBeNull();
    expect(screen.getByDisplayValue('5901')).toBeInTheDocument();

    // rdp is not supported by the image: its hint never lands anywhere.
    await openWorkspaceTab(en.admin.templatesPage.env);
    expect(screen.queryByDisplayValue('RDP_DOMAIN')).toBeNull();
    expect(screen.queryByRole('button', { name: /RDP_DOMAIN/ })).toBeNull();

    // requires closure: VNC_PW (vnc, relevant) pulls VNC_USER in as a
    // suggestion despite its kasmvnc-only protocols.
    expect(screen.getByRole('button', { name: /VNC_PW/ })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /VNC_USER/ })).toBeInTheDocument();
  });

  it('keeps configured protocols untouched and filters hints to them', async () => {
    const withVnc: TemplateInput = {
      ...initial,
      protocols: [{ name: 'vnc', port: 5901, default: true }],
    };
    renderWithProviders(<TemplateDialog isNew initial={withVnc} onClose={() => {}} />);

    await userEvent.click(
      screen.getByRole('button', { name: en.admin.templatesPage.imageCatalog }),
    );
    await userEvent.click(await screen.findByRole('option', { name: /Browsers/ }));
    await userEvent.click(await screen.findByRole('option', { name: /Firefox/ }));
    await userEvent.click(
      screen.getByRole('button', { name: en.admin.templatesPage.applyRecommendation }),
    );

    // The protocol list is not touched: no ssh tab was added.
    await openProtocolsTab();
    expect(screen.queryByRole('button', { name: /ssh/ })).toBeNull();

    // ssh-only hints are filtered out entirely (row or suggestion).
    await openWorkspaceTab(en.admin.templatesPage.env);
    expect(screen.queryByDisplayValue('WAAS_SSH_ENABLED')).toBeNull();
    expect(screen.queryByRole('button', { name: /WAAS_SSH_AUTHORIZED_KEYS_FILE/ })).toBeNull();

    // vnc hints still apply.
    expect(screen.getByRole('button', { name: /VNC_PW/ })).toBeInTheDocument();
  });

  it('adopts a suggestion on click: real row, description as value placeholder', async () => {
    renderWithProviders(<TemplateDialog isNew initial={initial} onClose={() => {}} />);

    await userEvent.click(
      screen.getByRole('button', { name: en.admin.templatesPage.imageCatalog }),
    );
    await userEvent.click(await screen.findByRole('option', { name: /Browsers/ }));
    await userEvent.click(await screen.findByRole('option', { name: /Firefox/ }));
    await userEvent.click(
      screen.getByRole('button', { name: en.admin.templatesPage.applyRecommendation }),
    );

    await openWorkspaceTab(en.admin.templatesPage.env);
    await userEvent.click(screen.getByRole('button', { name: /WAAS_SSH_AUTHORIZED_KEYS_FILE/ }));

    // Suggestion became a real (empty) row and left the suggestion list.
    const nameInput = screen.getByDisplayValue('WAAS_SSH_AUTHORIZED_KEYS_FILE');
    expect(nameInput).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /WAAS_SSH_AUTHORIZED_KEYS_FILE/ })).toBeNull();
    const row = nameInput.closest('div.flex') as HTMLElement;
    const valueInput = within(row).getByLabelText('value');
    expect(valueInput).toHaveValue('');
    expect(valueInput).toHaveAttribute('placeholder', 'Path to the authorized keys file');
  });

  it('never overwrites an already-present env entry, while still adding non-colliding hints', async () => {
    // Pre-existing entry collides with the fixture's WAAS_SSH_ENABLED hint
    // (default '0') but carries a different value — the documented
    // "merge by name without overwriting" guarantee must keep it as-is.
    const initialWithEnv: TemplateInput = {
      ...initial,
      env: [{ name: 'WAAS_SSH_ENABLED', value: '1' }],
    };
    renderWithProviders(<TemplateDialog isNew initial={initialWithEnv} onClose={() => {}} />);

    await userEvent.click(
      screen.getByRole('button', { name: en.admin.templatesPage.imageCatalog }),
    );
    await userEvent.click(await screen.findByRole('option', { name: /Browsers/ }));
    await userEvent.click(await screen.findByRole('option', { name: /Firefox/ }));
    await userEvent.click(
      screen.getByRole('button', { name: en.admin.templatesPage.applyRecommendation }),
    );

    // No duplicate row was appended for the colliding name.
    await openWorkspaceTab(en.admin.templatesPage.env);
    expect(screen.getAllByDisplayValue('WAAS_SSH_ENABLED')).toHaveLength(1);

    // The pre-existing value survives untouched (not clobbered to the
    // hint's default '0').
    const nameInput = screen.getByDisplayValue('WAAS_SSH_ENABLED');
    const row = nameInput.closest('div.flex') as HTMLElement;
    expect(within(row).getByLabelText('value')).toHaveValue('1');

    // The non-colliding no-default hint is still offered, as a suggestion.
    expect(
      screen.getByRole('button', { name: /WAAS_SSH_AUTHORIZED_KEYS_FILE/ }),
    ).toBeInTheDocument();
  });
});

describe('TemplateDialog — sectioned form', () => {
  it('a submit with invalid workload YAML jumps to Workspace › Workload', async () => {
    renderWithProviders(
      <TemplateDialog isNew initial={{ ...initial, image: 'img:1' }} onClose={() => {}} />,
    );

    // Type broken YAML into the (hidden) workload editor, then submit
    // from the default General section: the error must not stay burrowed
    // in an inactive tab.
    const textarea = document.querySelector('details textarea') as HTMLTextAreaElement;
    fireEvent.change(textarea, { target: { value: '{bad' } });
    await userEvent.click(screen.getByRole('button', { name: 'Save' }));

    expect(screen.getByText(en.admin.templatesPage.workloadInvalid)).toBeVisible();
    expect(textarea).toBeVisible();
  });
});

describe('TemplateDialog — architecture nodeSelector prefill', () => {
  const workloadYaml = () =>
    (document.querySelector('details')?.querySelector('textarea') as HTMLTextAreaElement).value;

  const selectImage = async (name: RegExp) => {
    await userEvent.click(await screen.findByRole('option', { name }));
  };

  it('stamps kubernetes.io/arch on single-arch selection and drops it on a multi-arch one', async () => {
    renderWithProviders(<TemplateDialog isNew initial={initial} onClose={() => {}} />);
    await userEvent.click(
      screen.getByRole('button', { name: en.admin.templatesPage.imageCatalog }),
    );
    await userEvent.click(await screen.findByRole('option', { name: /Browsers/ }));

    // Firefox is amd64-only: the label lands in the workload YAML and
    // the collapsed section opens to show it.
    await selectImage(/Firefox/);
    expect(workloadYaml()).toContain('kubernetes.io/arch: amd64');
    expect(document.querySelector('details')?.open).toBe(true);

    // Chromium is multi-arch: the stale constraint is removed (and the
    // now-empty workload text clears entirely).
    await userEvent.clear(screen.getByRole('textbox', { name: en.admin.templatesPage.image }));
    await selectImage(/Chromium/);
    expect(workloadYaml()).not.toContain('kubernetes.io/arch');
  });

  it('never touches other nodeSelector keys', async () => {
    const withWorkload: TemplateInput = {
      ...initial,
      workload: { nodeSelector: { zone: 'eu-west' } },
    };
    renderWithProviders(<TemplateDialog isNew initial={withWorkload} onClose={() => {}} />);
    await userEvent.click(
      screen.getByRole('button', { name: en.admin.templatesPage.imageCatalog }),
    );
    await userEvent.click(await screen.findByRole('option', { name: /Browsers/ }));

    await selectImage(/Firefox/);
    expect(workloadYaml()).toContain('zone: eu-west');
    expect(workloadYaml()).toContain('kubernetes.io/arch: amd64');

    await userEvent.clear(screen.getByRole('textbox', { name: en.admin.templatesPage.image }));
    await selectImage(/Chromium/);
    expect(workloadYaml()).toContain('zone: eu-west');
    expect(workloadYaml()).not.toContain('kubernetes.io/arch');
  });
});
