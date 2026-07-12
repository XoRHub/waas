// @vitest-environment jsdom
import { cleanup, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { AppIcon, ImageOptionCard } from './ImageOptionCard';

afterEach(cleanup);

describe('AppIcon', () => {
  it('renders the OS fallback without a slug', () => {
    render(<AppIcon os="windows" />);
    expect(document.querySelector('img')?.getAttribute('src')).toBe('/icons/os-windows.svg');
  });
});

describe('ImageOptionCard', () => {
  it('falls back to the linux icon for a template without a catalog icon', () => {
    render(<ImageOptionCard title="XFCE Desktop" os="linux" subtitle="linux · kasmvnc" />);
    expect(document.querySelector('img')?.getAttribute('src')).toBe('/icons/os-linux.svg');
    expect(screen.getByText('XFCE Desktop')).toBeTruthy();
    expect(screen.getByText('linux · kasmvnc')).toBeTruthy();
  });

  it('uses the catalog entry icon when vendored', () => {
    render(<ImageOptionCard title="Firefox" os="linux" icon="firefox" />);
    expect(document.querySelector('img')?.getAttribute('src')).toBe('/icons/firefox.svg');
  });

  it('selects on click and reflects the option state', async () => {
    const onSelect = vi.fn();
    render(<ImageOptionCard title="Firefox" selected onSelect={onSelect} />);
    const option = screen.getByRole('option', { name: /Firefox/ });
    expect(option.getAttribute('aria-selected')).toBe('true');
    await userEvent.click(option);
    expect(onSelect).toHaveBeenCalledOnce();
  });

  it('carries the full text in the native tooltip (truncation-safe)', () => {
    render(
      <ImageOptionCard
        title="Un très long nom de template qui sera tronqué"
        subtitle="linux · vnc/rdp/ssh"
      />,
    );
    const option = screen.getByRole('option');
    expect(option.getAttribute('title')).toBe(
      'Un très long nom de template qui sera tronqué\nlinux · vnc/rdp/ssh',
    );
  });

  it('includes the disabled reason in the tooltip', () => {
    render(
      <ImageOptionCard
        title="Firefox"
        subtitle="linux · vnc"
        disabled
        disabledReason="unavailable"
      />,
    );
    expect(screen.getByRole('option').getAttribute('title')).toBe(
      'Firefox\nlinux · vnc\nunavailable',
    );
  });

  it('disabled shows the reason and never fires onSelect', async () => {
    const onSelect = vi.fn();
    render(
      <ImageOptionCard
        title="Firefox"
        disabled
        disabledReason="unavailable (image not allowed by your policy)"
        onSelect={onSelect}
      />,
    );
    expect(screen.getByText('unavailable (image not allowed by your policy)')).toBeTruthy();
    await userEvent.click(screen.getByRole('option', { name: /Firefox/ }));
    expect(onSelect).not.toHaveBeenCalled();
  });
});
