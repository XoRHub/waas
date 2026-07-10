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
        live: false,
        default: 'false',
        description: 'audio',
      },
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
