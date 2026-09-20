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
    // rdp alongside vnc: the OS-bound rule must drop it on a linux
    // template; kasmvnc is dropped by exclusivity.
    protocols: ['vnc', 'rdp', 'kasmvnc'],
    discovered: [
      {
        image: 'ghcr.io/acme/firefox:128',
        app: 'firefox',
        displayName: 'Firefox',
        description: 'Managed, policy-hardened Firefox in a single-app kiosk session.',
        version: '128',
        os: 'linux',
        profile: 'hardened',
        architectures: ['amd64'],
        recommended: {
          podSecurityContext: { runAsUser: 1000 },
          securityContext: { readOnlyRootFilesystem: true },
          volumes: [{ name: 'tmp', mountPath: '/tmp' }],
          env: [
            { name: 'WAAS_VNC_RESOLUTION', default: '1920x1080', protocols: ['vnc'] },
            {
              name: 'WAAS_DESKTOP_PASSWORD',
              protocols: ['vnc'],
              description: 'Desktop password (generated when absent)',
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

/** Navigate the sectioned dialog: Workspace section, then one inner tab.
 * Regex names: the ● injection badge joins the tab's accessible name. */
const openWorkspaceTab = async (inner: string) => {
  await userEvent.click(
    screen.getByRole('button', { name: new RegExp(en.admin.templatesPage.tabWorkspace) }),
  );
  await userEvent.click(screen.getByRole('button', { name: new RegExp(inner) }));
};

const openProtocolsTab = async () => {
  await userEvent.click(screen.getByRole('button', { name: en.admin.templatesPage.protocols }));
};

describe('TemplateDialog — apply catalog recommendation', () => {
  it('prefills the workload YAML/env', async () => {
    renderWithProviders(<TemplateDialog isNew initial={initial} onClose={() => {}} />);

    await userEvent.click(
      screen.getByRole('button', { name: en.admin.templatesPage.imageCatalog }),
    );
    await userEvent.click(await screen.findByRole('option', { name: /Browsers/ }));
    await userEvent.click(await screen.findByRole('option', { name: /Firefox/ }));
    await userEvent.click(
      screen.getByRole('button', { name: en.admin.templatesPage.applyRecommendation }),
    );

    const workloadYaml = (
      document.querySelector('[data-panel="workload"] textarea') as HTMLTextAreaElement
    ).value;
    expect(workloadYaml).toContain('runAsUser: 1000');
    expect(workloadYaml).toContain('readOnlyRootFilesystem: true');
    expect(workloadYaml).toContain('mountPath: /tmp');
    // env goes to EnvFieldset's input.env, not the workload YAML.
    expect(workloadYaml).not.toContain('WAAS_VNC_RESOLUTION');
    await openWorkspaceTab(en.admin.templatesPage.env);
    expect(screen.getByDisplayValue('WAAS_VNC_RESOLUTION')).toBeInTheDocument();
    // A hint without a default never becomes a real row — it shows up
    // as a greyed suggestion instead (adoption tested separately).
    expect(screen.queryByDisplayValue('WAAS_DESKTOP_PASSWORD')).toBeNull();
    expect(screen.getByRole('button', { name: /WAAS_DESKTOP_PASSWORD/ })).toBeInTheDocument();
  });

  it('adds the OS-allowed image protocols on a protocol-less template, dropping rdp (OS rule) and kasmvnc (exclusivity)', async () => {
    renderWithProviders(<TemplateDialog isNew initial={initial} onClose={() => {}} />);

    await userEvent.click(
      screen.getByRole('button', { name: en.admin.templatesPage.imageCatalog }),
    );
    await userEvent.click(await screen.findByRole('option', { name: /Browsers/ }));
    await userEvent.click(await screen.findByRole('option', { name: /Firefox/ }));
    await userEvent.click(
      screen.getByRole('button', { name: en.admin.templatesPage.applyRecommendation }),
    );

    // Only the vnc tab appears (rdp dropped by the OS rule, kasmvnc by
    // exclusivity), active with its registry default port.
    await openProtocolsTab();
    expect(screen.getByRole('button', { name: /vnc/ })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /rdp/ })).toBeNull();
    expect(screen.queryByRole('button', { name: /kasmvnc/ })).toBeNull();
    expect(screen.getByDisplayValue('5901')).toBeInTheDocument();

    // rdp never made it onto the template: its hint never lands anywhere.
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

    // The protocol list is not touched: no kasmvnc/rdp tab was added.
    await openProtocolsTab();
    expect(screen.queryByRole('button', { name: /kasmvnc/ })).toBeNull();
    expect(screen.queryByRole('button', { name: /rdp/ })).toBeNull();

    // rdp-only hints are filtered out entirely (row or suggestion).
    await openWorkspaceTab(en.admin.templatesPage.env);
    expect(screen.queryByDisplayValue('RDP_DOMAIN')).toBeNull();
    expect(screen.queryByRole('button', { name: /RDP_DOMAIN/ })).toBeNull();

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
    await userEvent.click(screen.getByRole('button', { name: /WAAS_DESKTOP_PASSWORD/ }));

    // Suggestion became a real (empty) row and left the suggestion list.
    const nameInput = screen.getByDisplayValue('WAAS_DESKTOP_PASSWORD');
    expect(nameInput).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /WAAS_DESKTOP_PASSWORD/ })).toBeNull();
    const row = nameInput.closest('div.flex') as HTMLElement;
    const valueInput = within(row).getByLabelText('value');
    expect(valueInput).toHaveValue('');
    expect(valueInput).toHaveAttribute('placeholder', 'Desktop password (generated when absent)');
  });

  it('never overwrites an already-present env entry, while still adding non-colliding hints', async () => {
    // Pre-existing entry collides with the fixture's WAAS_VNC_RESOLUTION hint
    // (default '1920x1080') but carries a different value — the documented
    // "merge by name without overwriting" guarantee must keep it as-is.
    const initialWithEnv: TemplateInput = {
      ...initial,
      env: [{ name: 'WAAS_VNC_RESOLUTION', value: '1280x720' }],
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
    expect(screen.getAllByDisplayValue('WAAS_VNC_RESOLUTION')).toHaveLength(1);

    // The pre-existing value survives untouched (not clobbered to the
    // hint's default '1920x1080').
    const nameInput = screen.getByDisplayValue('WAAS_VNC_RESOLUTION');
    const row = nameInput.closest('div.flex') as HTMLElement;
    expect(within(row).getByLabelText('value')).toHaveValue('1280x720');

    // The non-colliding no-default hint is still offered, as a suggestion.
    expect(screen.getByRole('button', { name: /WAAS_DESKTOP_PASSWORD/ })).toBeInTheDocument();
  });
});

describe('TemplateDialog — identity prefill on catalog selection', () => {
  const selectFirefox = async () => {
    await userEvent.click(
      screen.getByRole('button', { name: en.admin.templatesPage.imageCatalog }),
    );
    await userEvent.click(await screen.findByRole('option', { name: /Browsers/ }));
    await userEvent.click(await screen.findByRole('option', { name: /Firefox/ }));
  };
  const displayNameInput = () =>
    screen.getByRole('textbox', { name: en.admin.templatesPage.displayName });
  const descriptionInput = () =>
    screen.getByRole('textbox', { name: en.admin.templatesPage.description });

  it('fills empty displayName/description from the discovered image', async () => {
    renderWithProviders(
      <TemplateDialog
        isNew
        initial={{ ...initial, displayName: '', description: '' }}
        onClose={() => {}}
      />,
    );
    await selectFirefox();
    expect(displayNameInput()).toHaveValue('Firefox');
    expect(descriptionInput()).toHaveValue(
      'Managed, policy-hardened Firefox in a single-app kiosk session.',
    );
  });

  it('never overwrites a field the admin filled; each field decided independently', async () => {
    // displayName already typed, description still empty: only the
    // description is prefilled.
    renderWithProviders(
      <TemplateDialog isNew initial={{ ...initial, description: '' }} onClose={() => {}} />,
    );
    await selectFirefox();
    expect(displayNameInput()).toHaveValue('My Template');
    expect(descriptionInput()).toHaveValue(
      'Managed, policy-hardened Firefox in a single-app kiosk session.',
    );
  });

  it('leaves both fields untouched when the admin filled them', async () => {
    renderWithProviders(
      <TemplateDialog
        isNew
        initial={{ ...initial, description: 'Hand-written description.' }}
        onClose={() => {}}
      />,
    );
    await selectFirefox();
    expect(displayNameInput()).toHaveValue('My Template');
    expect(descriptionInput()).toHaveValue('Hand-written description.');
  });
});

describe('TemplateDialog — sectioned form', () => {
  it('a catalog prefill marks the Workspace/Workload tabs with ●, cleared on visit', async () => {
    renderWithProviders(<TemplateDialog isNew initial={initial} onClose={() => {}} />);

    const workspaceTab = () =>
      screen.getByRole('button', { name: new RegExp(en.admin.templatesPage.tabWorkspace) });
    expect(workspaceTab().textContent).not.toContain('●');

    await userEvent.click(
      screen.getByRole('button', { name: en.admin.templatesPage.imageCatalog }),
    );
    await userEvent.click(await screen.findByRole('option', { name: /Browsers/ }));
    await userEvent.click(await screen.findByRole('option', { name: /Firefox/ }));
    await userEvent.click(
      screen.getByRole('button', { name: en.admin.templatesPage.applyRecommendation }),
    );

    // YAML and env rows/suggestions landed in hidden tabs: the section
    // and BOTH receiving tabs signal it.
    expect(workspaceTab().textContent).toContain('●');
    await userEvent.click(workspaceTab());
    const workloadTab = screen.getByRole('button', { name: /Workload \(advanced\)/ });
    const envTab = screen.getByRole('button', { name: /Environment/ });
    expect(workloadTab.textContent).toContain('●');
    expect(envTab.textContent).toContain('●');

    // Visiting acknowledges tab by tab; the section badge holds until
    // every touched tab was seen.
    await userEvent.click(workloadTab);
    expect(workloadTab.textContent).not.toContain('●');
    expect(workspaceTab().textContent).toContain('●');
    await userEvent.click(envTab);
    expect(envTab.textContent).not.toContain('●');
    expect(workspaceTab().textContent).not.toContain('●');
  });

  it('a submit with invalid workload YAML jumps to Workspace › Workload', async () => {
    renderWithProviders(
      <TemplateDialog isNew initial={{ ...initial, image: 'img:1' }} onClose={() => {}} />,
    );

    // Type broken YAML into the (hidden) workload editor, then submit
    // from the default General section: the error must not stay burrowed
    // in an inactive tab.
    const textarea = document.querySelector(
      '[data-panel="workload"] textarea',
    ) as HTMLTextAreaElement;
    fireEvent.change(textarea, { target: { value: '{bad' } });
    await userEvent.click(screen.getByRole('button', { name: 'Save' }));

    expect(screen.getByText(en.admin.templatesPage.workloadInvalid)).toBeVisible();
    expect(textarea).toBeVisible();
  });
});

describe('TemplateDialog — architecture nodeSelector prefill', () => {
  const workloadYaml = () =>
    (document.querySelector('[data-panel="workload"] textarea') as HTMLTextAreaElement).value;

  const selectImage = async (name: RegExp) => {
    await userEvent.click(await screen.findByRole('option', { name }));
  };

  it('stamps kubernetes.io/arch on single-arch selection and drops it on a multi-arch one', async () => {
    renderWithProviders(<TemplateDialog isNew initial={initial} onClose={() => {}} />);
    await userEvent.click(
      screen.getByRole('button', { name: en.admin.templatesPage.imageCatalog }),
    );
    await userEvent.click(await screen.findByRole('option', { name: /Browsers/ }));

    // Firefox is amd64-only: the label lands in the workload YAML.
    await selectImage(/Firefox/);
    expect(workloadYaml()).toContain('kubernetes.io/arch: amd64');

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
