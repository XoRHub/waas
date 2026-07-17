// @vitest-environment jsdom
import { useState } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@/test/render';
import { createApiMock } from '@/test/apiMock';
import en from '@/i18n/locales/en.json';
import type { CatalogImage, DeploymentRecommendation } from '@/types';
import { CatalogImageField } from './CatalogImageField';

const firefoxRecommendation: DeploymentRecommendation = {
  podSecurityContext: { runAsUser: 1000 },
  securityContext: { readOnlyRootFilesystem: true },
  volumes: [{ name: 'tmp', mountPath: '/tmp' }],
  env: [{ name: 'WAAS_SSH_ENABLED', protocols: ['ssh'] }],
};

const apiMock = createApiMock();
vi.mock('@/lib/api', () => ({
  get api() {
    return apiMock.api;
  },
}));

// One catalog per mode, plus a disabled one that must never be offered.
const catalogs: CatalogImage[] = [
  {
    name: 'browsers',
    displayName: 'Browsers',
    registry: 'ghcr.io/acme/',
    enabled: true,
    architectures: ['amd64', 'arm64'],
    discovered: [
      {
        image: 'ghcr.io/acme/firefox:128',
        app: 'firefox',
        displayName: 'Firefox',
        version: '128',
        os: 'linux',
        profile: 'hardened',
        architectures: ['amd64'],
        recommended: firefoxRecommendation,
      },
      {
        image: 'ghcr.io/acme/chromium:126',
        app: 'chromium',
        displayName: 'Chromium',
        version: '126',
        os: 'linux',
      },
    ],
  },
  {
    name: 'ubuntu',
    displayName: 'Ubuntu Desktop',
    image: 'docker.io/acme/ubuntu:24.04',
    enabled: true,
    architectures: ['arm64'],
  },
  {
    name: 'legacy',
    displayName: 'Legacy',
    image: 'docker.io/acme/legacy:1',
    enabled: false,
  },
];

// The image input is controlled by the parent (the value IS the fuzzy
// query), so the tests need the real round-trip, not a bare spy.
function Harness({
  initial,
  onChange,
  onApplyRecommendation,
  onArchitectures,
}: {
  initial: string;
  onChange: (v: string) => void;
  onApplyRecommendation?: (recommended: DeploymentRecommendation) => void;
  onArchitectures?: (architectures: string[]) => void;
}) {
  const [image, setImage] = useState(initial);
  return (
    <CatalogImageField
      image={image}
      onChange={(v) => {
        onChange(v);
        setImage(v);
      }}
      onApplyRecommendation={onApplyRecommendation}
      onArchitectures={onArchitectures}
    />
  );
}

function renderField(initial = '') {
  const onChange = vi.fn();
  const onApplyRecommendation = vi.fn();
  const onArchitectures = vi.fn();
  renderWithProviders(
    <Harness
      initial={initial}
      onChange={onChange}
      onApplyRecommendation={onApplyRecommendation}
      onArchitectures={onArchitectures}
    />,
  );
  return { onChange, onApplyRecommendation, onArchitectures };
}

// A string name is an exact match: never collides with "Image catalog".
const imageInput = () => screen.getByRole('textbox', { name: en.admin.templatesPage.image });

async function openPicker() {
  await userEvent.click(screen.getByRole('button', { name: en.admin.templatesPage.imageCatalog }));
}

beforeEach(() => {
  apiMock.route('/api/v1/admin/images', catalogs);
});

describe('CatalogImageField', () => {
  it('offers only enabled catalogs, with a free-input head option', async () => {
    renderField();
    await openPicker();
    await screen.findByRole('option', { name: /Browsers/ });
    expect(screen.getByRole('option', { name: en.admin.templatesPage.imageCatalogNone }));
    expect(screen.getByRole('option', { name: /Ubuntu Desktop/ }));
    expect(screen.queryByRole('option', { name: /Legacy/ })).toBeNull();
  });

  it('registry mode: typing in the image field fuzzy-filters, selecting fills the value', async () => {
    const { onChange } = renderField();
    await openPicker();
    await userEvent.click(await screen.findByRole('option', { name: /Browsers/ }));

    // The suggestions open on catalog selection, unfiltered.
    expect(screen.getByRole('option', { name: /Firefox/ }));
    await userEvent.type(imageInput(), 'chrom');
    expect(screen.queryByRole('option', { name: /Firefox/ })).toBeNull();

    await userEvent.click(screen.getByRole('option', { name: /Chromium/ }));
    expect(onChange).toHaveBeenLastCalledWith('ghcr.io/acme/chromium:126');
    expect(imageInput()).toHaveValue('ghcr.io/acme/chromium:126');
    // Selection closes the suggestions.
    expect(screen.queryByRole('listbox', { name: en.admin.templatesPage.image })).toBeNull();
  });

  it('single-image mode: selecting the catalog fills the image directly, no suggestions', async () => {
    const { onChange } = renderField();
    await openPicker();
    await userEvent.click(await screen.findByRole('option', { name: /Ubuntu Desktop/ }));
    expect(onChange).toHaveBeenCalledWith('docker.io/acme/ubuntu:24.04');
    expect(screen.queryByRole('listbox', { name: en.admin.templatesPage.image })).toBeNull();
  });

  it('a value matching no catalog entry is kept as-is, with the empty note shown', async () => {
    const { onChange } = renderField();
    await openPicker();
    await userEvent.click(await screen.findByRole('option', { name: /Browsers/ }));
    await userEvent.clear(imageInput());
    await userEvent.type(imageInput(), 'docker.io/zzz');
    expect(onChange).toHaveBeenLastCalledWith('docker.io/zzz');
    expect(screen.getByText(en.admin.templatesPage.imageSearchEmpty));
  });

  it('the none option hides the suggestions without touching the image value', async () => {
    const { onChange } = renderField('docker.io/current');
    await openPicker();
    await userEvent.click(await screen.findByRole('option', { name: /Browsers/ }));
    expect(screen.getByRole('listbox', { name: en.admin.templatesPage.image }));

    await openPicker();
    await userEvent.click(
      screen.getByRole('option', { name: en.admin.templatesPage.imageCatalogNone }),
    );
    expect(screen.queryByRole('listbox', { name: en.admin.templatesPage.image })).toBeNull();
    expect(onChange).not.toHaveBeenCalled();
    expect(imageInput()).toHaveValue('docker.io/current');
  });

  it('reports the per-image architectures on selection, entry-level as fallback', async () => {
    const { onArchitectures } = renderField();
    await openPicker();
    await userEvent.click(await screen.findByRole('option', { name: /Browsers/ }));

    // Firefox carries its own list.
    await userEvent.click(screen.getByRole('option', { name: /Firefox/ }));
    expect(onArchitectures).toHaveBeenLastCalledWith(['amd64']);

    // Chromium has none: the entry-level list applies.
    await userEvent.clear(imageInput());
    await userEvent.click(await screen.findByRole('option', { name: /Chromium/ }));
    expect(onArchitectures).toHaveBeenLastCalledWith(['amd64', 'arm64']);

    // Free typing never reports architectures.
    onArchitectures.mockClear();
    await userEvent.clear(imageInput());
    await userEvent.type(imageInput(), 'docker.io/zzz');
    expect(onArchitectures).not.toHaveBeenCalled();
  });

  it('single-image mode reports the entry-level architectures', async () => {
    const { onArchitectures } = renderField();
    await openPicker();
    await userEvent.click(await screen.findByRole('option', { name: /Ubuntu Desktop/ }));
    expect(onArchitectures).toHaveBeenCalledWith(['arm64']);
  });

  it('shows an apply-recommendation button only for a discovered image carrying one', async () => {
    const { onApplyRecommendation } = renderField();
    await openPicker();
    await userEvent.click(await screen.findByRole('option', { name: /Browsers/ }));

    // Firefox carries a recommendation: the button is offered once selected.
    await userEvent.click(screen.getByRole('option', { name: /Firefox/ }));
    const applyButton = screen.getByRole('button', {
      name: en.admin.templatesPage.applyRecommendation,
    });
    await userEvent.click(applyButton);
    expect(onApplyRecommendation).toHaveBeenCalledWith(firefoxRecommendation);

    // Chromium has no recommendation: no button once it's the current value.
    await userEvent.clear(imageInput());
    await userEvent.click(await screen.findByRole('option', { name: /Chromium/ }));
    expect(
      screen.queryByRole('button', { name: en.admin.templatesPage.applyRecommendation }),
    ).toBeNull();
  });
});
