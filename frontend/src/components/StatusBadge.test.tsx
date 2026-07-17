// @vitest-environment jsdom
import { describe, expect, it } from 'vitest';
import { screen } from '@testing-library/react';
import { renderWithProviders } from '@/test/render';
import en from '@/i18n/locales/en.json';
import type { EffectivePhase } from '@/lib/lifecycle';
import { StatusBadge } from './StatusBadge';

describe('StatusBadge', () => {
  it('renders the translated phase with its traffic-light color', () => {
    renderWithProviders(<StatusBadge phase="Running" />);
    expect(screen.getByText(en.portal.phase.Running)).toHaveClass('bg-green-100');
  });

  it('derived transitional phases go amber', () => {
    renderWithProviders(<StatusBadge phase="Pausing" />);
    expect(screen.getByText(en.portal.phase.Pausing)).toHaveClass('bg-amber-100');
  });

  it('falls back to the Stopped style on a phase outside the map', () => {
    renderWithProviders(<StatusBadge phase={'Unknown' as EffectivePhase} />);
    // Unknown phase: style falls back, the label echoes the raw key.
    expect(screen.getByText(/Unknown/)).toHaveClass('bg-slate-200');
  });
});
