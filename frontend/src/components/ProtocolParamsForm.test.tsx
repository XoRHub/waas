// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest';
import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@/test/render';
import type { ProtocolMeta } from '@/types';
import { audioEnabled, ProtocolParamsForm } from './ProtocolTabs';

const meta: ProtocolMeta[] = [
  {
    name: 'vnc',
    params: [
      {
        name: 'enable-audio',
        protocols: ['vnc'],
        kind: 'bool',
        tier: 'ui',
        category: 'audio',
        live: false,
        default: 'false',
        description: 'audio',
      },
    ],
  },
];

const param = (
  name: string,
  tier: 'ui' | 'advanced',
  category: 'display' | 'audio',
): ProtocolMeta['params'][number] => ({
  name,
  protocols: ['vnc'],
  kind: 'string',
  tier,
  category,
  live: false,
  description: `${name} hint`,
});

// Two sections; only "display" carries an advanced param, so only it may
// render a disclosure.
const sectionedMeta: ProtocolMeta[] = [
  {
    name: 'vnc',
    params: [
      param('color-depth', 'ui', 'display'),
      param('encodings', 'advanced', 'display'),
      param('audio-servername', 'ui', 'audio'),
    ],
  },
];

describe('audioEnabled — the enable-audio ⇄ audio-port condition', () => {
  it('is false for non-vnc protocols whatever the values', () => {
    expect(audioEnabled('rdp', { 'enable-audio': 'true' })).toBe(false);
  });
  it('follows the form value', () => {
    expect(audioEnabled('vnc', { 'enable-audio': 'true' })).toBe(true);
    expect(audioEnabled('vnc', { 'enable-audio': 'false' })).toBe(false);
    expect(audioEnabled('vnc', {})).toBe(false);
  });
  it('falls back to the template-locked value when the form inherits', () => {
    expect(audioEnabled('vnc', {}, { 'enable-audio': 'true' })).toBe(true);
    // An explicit user "false" wins over the locked "true".
    expect(audioEnabled('vnc', { 'enable-audio': 'false' }, { 'enable-audio': 'true' })).toBe(
      false,
    );
  });
});

describe('ProtocolParamsForm — sectioned rendering (Feature 7)', () => {
  it('renders one titled section per category, in payload order', () => {
    renderWithProviders(
      <ProtocolParamsForm meta={sectionedMeta} protocol="vnc" values={{}} onChange={() => {}} />,
    );
    const headings = screen.getAllByRole('heading', { level: 4 });
    expect(headings.map((h) => h.textContent)).toEqual(['Display', 'Audio']);
  });

  it('shows simple params immediately and advanced ones only behind their section disclosure', async () => {
    renderWithProviders(
      <ProtocolParamsForm meta={sectionedMeta} protocol="vnc" values={{}} onChange={() => {}} />,
    );
    expect(screen.getByText('color-depth')).toBeInTheDocument();
    expect(screen.getByText('audio-servername')).toBeInTheDocument();
    expect(screen.queryByText('encodings')).not.toBeInTheDocument();
    // Only the display section carries advanced params → single disclosure.
    const disclosures = screen.getAllByRole('checkbox', { name: 'Show advanced parameters' });
    expect(disclosures).toHaveLength(1);
    await userEvent.click(disclosures[0]);
    expect(screen.getByText('encodings')).toBeInTheDocument();
  });

  it('keeps other sections closed when one disclosure opens', async () => {
    const twoAdvanced: ProtocolMeta[] = [
      {
        name: 'vnc',
        params: [
          param('color-depth', 'ui', 'display'),
          param('encodings', 'advanced', 'display'),
          param('audio-servername', 'advanced', 'audio'),
        ],
      },
    ];
    renderWithProviders(
      <ProtocolParamsForm meta={twoAdvanced} protocol="vnc" values={{}} onChange={() => {}} />,
    );
    const disclosures = screen.getAllByRole('checkbox', { name: 'Show advanced parameters' });
    expect(disclosures).toHaveLength(2);
    await userEvent.click(disclosures[0]); // display's disclosure
    expect(screen.getByText('encodings')).toBeInTheDocument();
    expect(screen.queryByText('audio-servername')).not.toBeInTheDocument();
  });

  // Placement is purely tier-driven: the resolved allow-list only
  // filters, it never moves a ui-tier name behind the disclosure.
  it('keeps allow-listed ui-tier params visible without a disclosure', () => {
    renderWithProviders(
      <ProtocolParamsForm
        meta={sectionedMeta}
        protocol="vnc"
        values={{}}
        onChange={() => {}}
        allowList={['color-depth', 'audio-servername']}
      />,
    );
    expect(screen.getByText('color-depth')).toBeInTheDocument();
    expect(screen.getByText('audio-servername')).toBeInTheDocument();
    // encodings (advanced tier) is outside the allow-list: no section
    // has advanced content left, so no disclosure renders at all.
    expect(
      screen.queryByRole('checkbox', { name: 'Show advanced parameters' }),
    ).not.toBeInTheDocument();
  });

  it('renders the per-section extra control in template-editor mode', () => {
    renderWithProviders(
      <ProtocolParamsForm
        meta={sectionedMeta}
        protocol="vnc"
        values={{}}
        onChange={() => {}}
        renderSectionExtra={(category) => <span>extra-{category}</span>}
      />,
    );
    expect(screen.getByText('extra-display')).toBeInTheDocument();
    expect(screen.getByText('extra-audio')).toBeInTheDocument();
  });

  it('falls back to the no-tunable-params message when every section is empty', () => {
    renderWithProviders(
      <ProtocolParamsForm
        meta={sectionedMeta}
        protocol="vnc"
        values={{}}
        onChange={() => {}}
        allowList={[]}
      />,
    );
    expect(
      screen.getByText('This protocol has no user-tunable parameters on this template.'),
    ).toBeInTheDocument();
  });
});

describe('ProtocolParamsForm — kasmvnc admin-managed config (read-only)', () => {
  // kasmvnc has no registry entry on purpose (its config does not travel
  // through guacd), but a config exists and applies: the form must show
  // it read-only instead of the misleading "no tunable parameters".
  it('shows the config content instead of the no-tunable-params message', () => {
    renderWithProviders(
      <ProtocolParamsForm
        meta={[]}
        protocol="kasmvnc"
        values={{}}
        onChange={() => {}}
        kasmvncConfig={{
          content: 'desktop:\n  resolution:\n    width: 1280\n',
          variant: 'template',
        }}
      />,
    );
    expect(
      screen.getByText('KasmVNC configuration (managed by the administrator)'),
    ).toBeInTheDocument();
    expect(screen.getByText(/width: 1280/)).toBeInTheDocument();
    expect(
      screen.queryByText('This protocol has no user-tunable parameters on this template.'),
    ).not.toBeInTheDocument();
    // Read-only: the YamlEditor textbox blocks typing, and no other
    // editable control is rendered for it.
    expect(screen.getByRole('textbox')).toHaveAttribute('readonly');
    expect(screen.queryByRole('checkbox')).not.toBeInTheDocument();
  });

  it('labels the effective variant differently from the template one', () => {
    renderWithProviders(
      <ProtocolParamsForm
        meta={[]}
        protocol="kasmvnc"
        values={{}}
        onChange={() => {}}
        kasmvncConfig={{ content: 'x: 1\n', variant: 'effective' }}
      />,
    );
    expect(screen.getByText(/actually applied to this workspace/)).toBeInTheDocument();
  });

  it('tells that image defaults apply when the admin config is empty', () => {
    renderWithProviders(
      <ProtocolParamsForm
        meta={[]}
        protocol="kasmvnc"
        values={{}}
        onChange={() => {}}
        kasmvncConfig={{ content: '', variant: 'template' }}
      />,
    );
    expect(
      screen.getByText('No custom configuration — the image defaults apply.'),
    ).toBeInTheDocument();
  });

  it('keeps the no-tunable-params message when no config is provided (remote machines)', () => {
    renderWithProviders(
      <ProtocolParamsForm meta={[]} protocol="kasmvnc" values={{}} onChange={() => {}} />,
    );
    expect(
      screen.getByText('This protocol has no user-tunable parameters on this template.'),
    ).toBeInTheDocument();
  });
});

describe('ProtocolParamsForm — conditional audio-port section', () => {
  it('shows nothing audio-related while enable-audio is off', () => {
    renderWithProviders(
      <ProtocolParamsForm
        meta={meta}
        protocol="vnc"
        values={{}}
        onChange={() => {}}
        audioPortExposed={false}
      />,
    );
    expect(screen.queryByText(/audio port \(4713\)/)).not.toBeInTheDocument();
  });

  it('warns when enable-audio is on but the template does not expose the port', () => {
    renderWithProviders(
      <ProtocolParamsForm
        meta={meta}
        protocol="vnc"
        values={{ 'enable-audio': 'true' }}
        onChange={() => {}}
        audioPortExposed={false}
      />,
    );
    expect(screen.getByText(/does not expose the audio port \(4713\)/)).toBeInTheDocument();
  });

  it('confirms when enable-audio is on and the port is exposed', () => {
    renderWithProviders(
      <ProtocolParamsForm
        meta={meta}
        protocol="vnc"
        values={{ 'enable-audio': 'true' }}
        onChange={() => {}}
        audioPortExposed
      />,
    );
    expect(screen.getByText(/exposes the audio port \(4713\)/)).toBeInTheDocument();
  });

  it('renders inside the audio section when one exists, not at the bottom of the form', () => {
    renderWithProviders(
      <ProtocolParamsForm
        meta={sectionedMeta}
        protocol="vnc"
        values={{ 'enable-audio': 'true' }}
        onChange={() => {}}
        audioPortExposed
      />,
    );
    const status = screen.getByText(/exposes the audio port \(4713\)/);
    const section = status.closest('section');
    expect(section).not.toBeNull();
    expect(section).toContainElement(screen.getByRole('heading', { level: 4, name: 'Audio' }));
  });

  it('falls back to the bottom of the form when no audio section renders', () => {
    // Allow-list keeps only a display param: no audio section, but the
    // template-locked enable-audio still calls for the port status.
    renderWithProviders(
      <ProtocolParamsForm
        meta={sectionedMeta}
        protocol="vnc"
        values={{}}
        onChange={() => {}}
        allowList={['color-depth']}
        placeholders={{ 'enable-audio': 'true' }}
        audioPortExposed={false}
      />,
    );
    const status = screen.getByText(/does not expose the audio port \(4713\)/);
    expect(status.closest('section')).toBeNull();
  });

  it('renders the editable checkbox in template-editor mode and reports toggles', async () => {
    const onAudioPortChange = vi.fn();
    renderWithProviders(
      <ProtocolParamsForm
        meta={meta}
        protocol="vnc"
        values={{ 'enable-audio': 'true' }}
        onChange={() => {}}
        audioPortExposed={false}
        onAudioPortChange={onAudioPortChange}
      />,
    );
    const checkbox = screen.getByRole('checkbox', { name: /Expose the audio port \(4713\)/ });
    await userEvent.click(checkbox);
    expect(onAudioPortChange).toHaveBeenCalledWith(true);
  });
});
