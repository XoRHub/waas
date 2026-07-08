// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest';
import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@/test/render';
import { OpenChoiceDialog } from './OpenChoiceDialog';

describe('OpenChoiceDialog', () => {
  it('reports the choice with remember=true by default', async () => {
    const onChoice = vi.fn();
    renderWithProviders(<OpenChoiceDialog onChoice={onChoice} onClose={() => {}} />);

    await userEvent.click(screen.getByRole('button', { name: 'New tab' }));
    expect(onChoice).toHaveBeenCalledWith(true, true);
  });

  it('reports same-tab without persisting when remember is unchecked', async () => {
    const onChoice = vi.fn();
    renderWithProviders(<OpenChoiceDialog onChoice={onChoice} onClose={() => {}} />);

    await userEvent.click(screen.getByRole('checkbox', { name: 'Remember my choice' }));
    await userEvent.click(screen.getByRole('button', { name: 'This tab' }));
    expect(onChoice).toHaveBeenCalledWith(false, false);
  });

  it('escape closes without choosing', async () => {
    const onChoice = vi.fn();
    const onClose = vi.fn();
    renderWithProviders(<OpenChoiceDialog onChoice={onChoice} onClose={onClose} />);

    await userEvent.keyboard('{Escape}');
    expect(onClose).toHaveBeenCalled();
    expect(onChoice).not.toHaveBeenCalled();
  });
});
