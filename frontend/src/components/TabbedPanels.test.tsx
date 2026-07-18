// @vitest-environment jsdom
import { useState } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@/test/render';
import { TabbedPanels, type PanelTab } from './TabbedPanels';

const twoTabs: PanelTab[] = [
  { id: 'a', label: 'Alpha', content: <p>alpha content</p> },
  { id: 'b', label: 'Beta', content: <input aria-label="beta-input" /> },
];

describe('TabbedPanels', () => {
  it('switches panels, keeping inactive ones mounted but hidden', async () => {
    renderWithProviders(<TabbedPanels tabs={twoTabs} />);

    expect(screen.getByText('alpha content')).toBeVisible();
    // Mounted-but-hidden: drafts and validation state must survive.
    const beta = screen.getByLabelText('beta-input');
    expect(beta).not.toBeVisible();

    await userEvent.type(beta, 'draft');
    await userEvent.click(screen.getByRole('button', { name: 'Beta' }));
    expect(screen.getByText('alpha content')).not.toBeVisible();
    expect(screen.getByLabelText('beta-input')).toHaveValue('draft');
  });

  it('marks locked tabs and drops them under hideLocked', () => {
    const tabs: PanelTab[] = [
      ...twoTabs,
      { id: 'c', label: 'Gamma', locked: true, content: <p>gamma</p> },
    ];
    const { unmount } = renderWithProviders(<TabbedPanels tabs={tabs} />);
    expect(screen.getByRole('button', { name: /Gamma/ }).textContent).toContain('🔒');
    unmount();

    renderWithProviders(<TabbedPanels tabs={tabs} hideLocked />);
    expect(screen.queryByRole('button', { name: /Gamma/ })).toBeNull();
  });

  it('falls back to the first tab when the active one disappears', async () => {
    // Same shape as rights loading async: the tab list shrinks in place.
    function Harness() {
      const [all, setAll] = useState(true);
      return (
        <>
          <TabbedPanels tabs={all ? twoTabs : [twoTabs[0]]} />
          <button type="button" onClick={() => setAll(false)}>
            shrink
          </button>
        </>
      );
    }
    renderWithProviders(<Harness />);

    await userEvent.click(screen.getByRole('button', { name: 'Beta' }));
    expect(screen.getByLabelText('beta-input')).toBeVisible();

    await userEvent.click(screen.getByRole('button', { name: 'shrink' }));
    expect(screen.getByText('alpha content')).toBeVisible();
  });

  it('supports controlled mode', async () => {
    const onSelect = vi.fn();
    renderWithProviders(<TabbedPanels tabs={twoTabs} active="b" onSelect={onSelect} />);

    expect(screen.getByLabelText('beta-input')).toBeVisible();
    await userEvent.click(screen.getByRole('button', { name: 'Alpha' }));
    expect(onSelect).toHaveBeenCalledWith('a');
  });

  it('shows clickable overflow indicators when the single-line row scrolls', () => {
    renderWithProviders(<TabbedPanels tabs={twoTabs} />);
    const row = screen.getByRole('button', { name: 'Alpha' }).parentElement as HTMLElement;
    row.scrollBy = vi.fn();

    // No layout in jsdom: simulate a row wider than its viewport.
    Object.defineProperty(row, 'scrollWidth', { value: 600, configurable: true });
    Object.defineProperty(row, 'clientWidth', { value: 200, configurable: true });
    fireEvent.scroll(row);

    // At the far left only the right indicator shows; clicking it scrolls.
    expect(screen.queryByRole('button', { name: 'More tabs to the left' })).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'More tabs to the right' }));
    expect(row.scrollBy).toHaveBeenCalledWith({ left: 160, behavior: 'smooth' });

    // Scrolled into the middle: both sides overflow.
    Object.defineProperty(row, 'scrollLeft', { value: 100, configurable: true });
    fireEvent.scroll(row);
    expect(screen.getByRole('button', { name: 'More tabs to the left' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'More tabs to the right' })).toBeInTheDocument();
  });

  it('activates the panel owning an invalid control on submit', () => {
    renderWithProviders(
      <form>
        <TabbedPanels
          tabs={[
            twoTabs[0],
            {
              id: 'req',
              label: 'Required',
              content: <input aria-label="req-input" required />,
            },
          ]}
        />
      </form>,
    );
    expect(screen.getByText('alpha content')).toBeVisible();

    // What the browser does on submit with an invalid hidden control.
    fireEvent.invalid(screen.getByLabelText('req-input'));
    expect(screen.getByLabelText('req-input')).toBeVisible();
    expect(screen.getByText('alpha content')).not.toBeVisible();
  });
});
