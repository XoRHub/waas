// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import { DASHBOARD_ICONS_CDN } from '@/lib/icon';
import { AppIcon, ImageOptionCard } from './ImageOptionCard';

// The profile badge renders translated text; the language detector must
// not pick the developer's locale.
void i18n.changeLanguage('en');

afterEach(cleanup);

describe('AppIcon', () => {
  it('renders the OS fallback without a slug', () => {
    render(<AppIcon os="windows" />);
    expect(document.querySelector('img')?.getAttribute('src')).toBe('/icons/os-windows.svg');
  });

  it('renders the OS fallback directly for an invalid slug (no CDN attempt)', () => {
    render(<AppIcon icon="../evil" os="linux" />);
    expect(document.querySelector('img')?.getAttribute('src')).toBe('/icons/os-linux.svg');
  });

  it('swaps to the OS fallback when the CDN load fails', () => {
    render(<AppIcon icon="no-such-slug" os="windows" />);
    const img = document.querySelector('img')!;
    expect(img.getAttribute('src')).toBe(`${DASHBOARD_ICONS_CDN}/no-such-slug.svg`);
    fireEvent.error(img);
    expect(img.getAttribute('src')).toBe('/icons/os-windows.svg');
  });

  it('does not loop when the fallback itself fails to load', () => {
    render(<AppIcon icon="no-such-slug" os="linux" />);
    const img = document.querySelector('img')!;
    fireEvent.error(img);
    expect(img.getAttribute('src')).toBe('/icons/os-linux.svg');
    fireEvent.error(img);
    expect(img.getAttribute('src')).toBe('/icons/os-linux.svg');
  });
});

describe('ImageOptionCard', () => {
  it('falls back to the linux icon for a template without a catalog icon', () => {
    render(<ImageOptionCard title="XFCE Desktop" os="linux" subtitle="linux · kasmvnc" />);
    expect(document.querySelector('img')?.getAttribute('src')).toBe('/icons/os-linux.svg');
    expect(screen.getByText('XFCE Desktop')).toBeTruthy();
    expect(screen.getByText('linux · kasmvnc')).toBeTruthy();
  });

  it('loads the catalog entry icon from the dashboard-icons CDN', () => {
    render(<ImageOptionCard title="Firefox" os="linux" icon="firefox" />);
    expect(document.querySelector('img')?.getAttribute('src')).toBe(
      `${DASHBOARD_ICONS_CDN}/firefox.svg`,
    );
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

  it('renders the Hardened badge for profile="hardened"', () => {
    render(<ImageOptionCard title="Ubuntu XFCE" profile="hardened" />);
    expect(screen.getByText('Hardened')).toBeTruthy();
  });

  it('renders the Normal badge for profile="normal"', () => {
    render(<ImageOptionCard title="Ubuntu XFCE" profile="normal" />);
    expect(screen.getByText('Normal')).toBeTruthy();
  });

  it('renders no badge for an unrecognized profile value (stale-row defense-in-depth)', () => {
    render(<ImageOptionCard title="Ubuntu XFCE" profile="banana" />);
    expect(screen.queryByText('Hardened')).toBeNull();
    expect(screen.queryByText('Normal')).toBeNull();
  });
});
