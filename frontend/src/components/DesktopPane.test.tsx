// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, render, screen, waitFor } from '@testing-library/react';
import { createApiMock } from '@/test/apiMock';
import type { ConnectResult } from '@/types';
import { DesktopPane } from './DesktopPane';

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
