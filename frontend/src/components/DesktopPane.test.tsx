// @vitest-environment jsdom
import { createRef } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { createApiMock } from '@/test/apiMock';
import type { ConnectResult, SessionCapabilities } from '@/types';
import { DesktopPane, type DesktopPaneHandle } from './DesktopPane';

const apiMock = createApiMock();
vi.mock('@/lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/api')>()),
  get api() {
    return apiMock.api;
  },
}));

/** jsdom has no ResizeObserver; the pane never fires it in this test. */
class FakeResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}
vi.stubGlobal('ResizeObserver', FakeResizeObserver);

// Fake guacamole-common-js: just enough surface for DesktopPane to connect
// and for the test to grab the Client it creates and drive its Display's
// onresize hook. `instances` is exposed via vi.hoisted so the test (which
// runs after the mock factory, due to hoisting) can read the Client the
// component created.
const guac = vi.hoisted(() => {
  class FakeDisplay {
    width = 0;
    height = 0;
    scale = vi.fn();
    onresize: ((width: number, height: number) => void) | null = null;
    getWidth() {
      return this.width;
    }
    getHeight() {
      return this.height;
    }
    getScale() {
      return 1;
    }
    getElement() {
      return document.createElement('div');
    }
  }
  const instances: FakeClient[] = [];
  class FakeClient {
    display = new FakeDisplay();
    onstatechange: ((state: number) => void) | null = null;
    onerror: (() => void) | null = null;
    onclipboard: unknown = null;
    constructor() {
      instances.push(this);
    }
    getDisplay() {
      return this.display;
    }
    connect() {}
    disconnect() {}
    sendMouseState() {}
    sendKeyEvent() {}
    createClipboardStream() {
      return { sendBlob: vi.fn(), sendEnd: vi.fn() };
    }
  }
  class FakeTunnel {
    sendMessage() {}
  }
  class FakeMouse {
    onmousedown: unknown = null;
    onmouseup: unknown = null;
    onmousemove: unknown = null;
  }
  class FakeKeyboard {
    onkeydown: unknown = null;
    onkeyup: unknown = null;
    reset() {}
  }
  return { instances, FakeClient, FakeTunnel, FakeMouse, FakeKeyboard };
});

vi.mock('guacamole-common-js', () => ({
  default: {
    WebSocketTunnel: guac.FakeTunnel,
    Client: guac.FakeClient,
    Mouse: guac.FakeMouse,
    Keyboard: guac.FakeKeyboard,
  },
}));

const connectResult: ConnectResult = {
  sessionId: 's1',
  connectionToken: 'tok',
  protocol: 'vnc',
};

afterEach(() => {
  cleanup();
  guac.instances.length = 0;
  apiMock.api.post.mockClear();
  vi.restoreAllMocks();
});

describe('DesktopPane', () => {
  it('rescales when the remote display reports a size (display.onresize), not only on pane resize', async () => {
    apiMock.api.post.mockResolvedValue({ data: connectResult });

    render(<DesktopPane workspaceId="w1" />);

    await waitFor(() => expect(guac.instances).toHaveLength(1));
    const client = guac.instances[0];
    const display = client.getDisplay();

    // The wiring under test: DesktopPane must have set this hook so a
    // guacd-driven size change (first `size` instruction, or a
    // server-side RandR resize) triggers a rescale even though no pane
    // ResizeObserver event fired.
    expect(display.onresize).toBeTypeOf('function');

    const container = screen.getByRole('application');
    Object.defineProperty(container, 'clientWidth', { value: 800, configurable: true });
    Object.defineProperty(container, 'clientHeight', { value: 600, configurable: true });

    display.width = 1000;
    display.height = 500;
    display.onresize?.(1000, 500);

    expect(display.scale).toHaveBeenCalledWith(Math.min(800 / 1000, 600 / 500));
  });
});

describe('DesktopPane kasmvnc (KasmVNC client in an iframe)', () => {
  const renderKasm = async (
    capabilities: SessionCapabilities = { clipboardCopy: true, clipboardPaste: true },
    autoFocus = false,
  ) => {
    apiMock.api.post.mockResolvedValue({
      data: { sessionId: 's1', connectionToken: 'tok', protocol: 'kasmvnc', capabilities },
    });
    const pane = createRef<DesktopPaneHandle>();
    const onStateChange = vi.fn();
    const { container } = render(
      <DesktopPane
        ref={pane}
        workspaceId="w1"
        onStateChange={onStateChange}
        // eslint-disable-next-line jsx-a11y/no-autofocus -- the prop under test
        autoFocus={autoFocus}
      />,
    );
    await waitFor(() => expect(container.querySelector('iframe')).not.toBeNull());
    const frame = container.querySelector('iframe')!;
    const fromClient = (data: unknown, opts: { source?: Window; origin?: string } = {}) =>
      act(() => {
        window.dispatchEvent(
          new MessageEvent('message', {
            data,
            source: opts.source ?? frame.contentWindow,
            origin: opts.origin ?? window.location.origin,
          }),
        );
      });
    return { pane, onStateChange, container, frame, fromClient };
  };

  it('drives the pane state from the client connection_state, not from onLoad', async () => {
    const { onStateChange, container, frame, fromClient } = await renderKasm();

    fireEvent.load(frame);
    expect(onStateChange).not.toHaveBeenCalledWith('connected');

    fromClient({ action: 'connection_state', value: 'connected' });
    expect(onStateChange).toHaveBeenLastCalledWith('connected');

    // The client's own auto-reconnect is transient: back to connecting.
    fromClient({ action: 'connection_state', value: 'reconnecting' });
    expect(onStateChange).toHaveBeenLastCalledWith('connecting');

    // 'disconnected' is final: the iframe goes, the session is over.
    fromClient({ action: 'connection_state', value: 'disconnected' });
    expect(onStateChange).toHaveBeenLastCalledWith('disconnected');
    expect(container.querySelector('iframe')).toBeNull();
  });

  it('keeps the iframe visible while connecting (the client focuses itself on connect)', async () => {
    const { frame } = await renderKasm();

    expect(frame.className).not.toContain('invisible');
    expect(getComputedStyle(frame).visibility).not.toBe('hidden');
    expect(screen.getByText('connect.connecting')).toBeTruthy();
  });

  it('focuses the iframe on connect when autoFocus is set', async () => {
    const { frame, fromClient } = await renderKasm(undefined, true);

    fromClient({ action: 'connection_state', value: 'connected' });

    expect(document.activeElement).toBe(frame);
  });

  it('ends the session on idle_session_timeout', async () => {
    const { onStateChange, container, fromClient } = await renderKasm();

    fromClient({ action: 'idle_session_timeout', value: 'Idle session timeout exceeded' });

    expect(onStateChange).toHaveBeenLastCalledWith('disconnected');
    expect(container.querySelector('iframe')).toBeNull();
  });

  it('ignores messages from another window or another origin', async () => {
    const { onStateChange, fromClient } = await renderKasm();

    fromClient({ action: 'connection_state', value: 'connected' }, { source: window });
    fromClient(
      { action: 'connection_state', value: 'connected' },
      { origin: 'https://evil.example' },
    );

    expect(onStateChange).not.toHaveBeenCalledWith('connected');
  });

  it('fails when wwt answers instead of the client (non-HTML document)', async () => {
    const { onStateChange, container, frame } = await renderKasm();
    Object.defineProperty(frame.contentDocument!, 'contentType', { value: 'text/plain' });

    fireEvent.load(frame);

    expect(onStateChange).toHaveBeenLastCalledWith('failed');
    expect(container.querySelector('iframe')).toBeNull();
  });

  it.each([true, false])('relays clipboardrx only when copy is allowed (%s)', async (allowed) => {
    const { pane, fromClient } = await renderKasm({ clipboardCopy: allowed, clipboardPaste: true });

    fromClient({ action: 'clipboardrx', value: 'from desktop' });

    expect(pane.current!.readRemoteClipboard()).toBe(allowed ? 'from desktop' : '');
  });

  it.each([true, false])('sends clipboardsnd only when paste is allowed (%s)', async (allowed) => {
    const { pane, frame } = await renderKasm({ clipboardCopy: true, clipboardPaste: allowed });
    const post = vi.spyOn(frame.contentWindow!, 'postMessage');

    pane.current!.sendClipboard('to desktop');

    if (allowed) {
      expect(post).toHaveBeenCalledWith(
        { action: 'clipboardsnd', value: 'to desktop' },
        window.location.origin,
      );
    } else {
      expect(post).not.toHaveBeenCalled();
    }
  });

  it('applies the quality live and keeps it across a reconnect', async () => {
    const { pane, frame, container } = await renderKasm();
    const post = vi.spyOn(frame.contentWindow!, 'postMessage');

    pane.current!.setQuality(3);
    expect(post).toHaveBeenCalledWith(
      { action: 'setvideoquality', value: 3 },
      window.location.origin,
    );
    expect(pane.current!.readQuality()).toBe(3);

    act(() => pane.current!.reconnect());
    await waitFor(() => expect(apiMock.api.post).toHaveBeenCalledTimes(2));
    await waitFor(() =>
      expect(container.querySelector('iframe')?.getAttribute('src')).toContain('video_quality=3'),
    );
  });

  it('reconnect after a final disconnect opens a fresh session', async () => {
    const { pane, container, fromClient } = await renderKasm();
    fromClient({ action: 'connection_state', value: 'disconnected' });
    expect(container.querySelector('iframe')).toBeNull();

    act(() => pane.current!.reconnect());

    await waitFor(() => expect(apiMock.api.post).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(container.querySelector('iframe')).not.toBeNull());
  });

  it('replays iframe keystrokes on the portal window so its shortcuts work', async () => {
    const { frame } = await renderKasm();
    const win = frame.contentWindow! as typeof window;
    // Stand-in for the overlay's window-level Ctrl+Alt+M handler.
    const shortcut = (e: KeyboardEvent) => {
      if (e.ctrlKey && e.altKey && e.key === 'm') e.preventDefault();
    };
    window.addEventListener('keydown', shortcut);
    // Stand-in for the client's own keyboard handler (inside the iframe).
    const desktop = vi.fn();
    win.document.addEventListener('keydown', desktop);
    const press = (init: KeyboardEventInit) => {
      const ev = new win.KeyboardEvent('keydown', { ...init, bubbles: true, cancelable: true });
      win.document.dispatchEvent(ev);
      return ev;
    };

    // Before load: nothing listens on the client's window yet.
    expect(press({ key: 'm', ctrlKey: true, altKey: true }).defaultPrevented).toBe(false);
    expect(desktop).toHaveBeenCalledTimes(1);

    fireEvent.load(frame);
    // The portal consumed it: kept from the desktop.
    expect(press({ key: 'm', ctrlKey: true, altKey: true }).defaultPrevented).toBe(true);
    expect(desktop).toHaveBeenCalledTimes(1);
    // Anything else still reaches the desktop untouched.
    expect(press({ key: 'a' }).defaultPrevented).toBe(false);
    expect(desktop).toHaveBeenCalledTimes(2);

    window.removeEventListener('keydown', shortcut);
  });
});
