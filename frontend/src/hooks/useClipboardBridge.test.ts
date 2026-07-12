// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';
import { act, cleanup, renderHook } from '@testing-library/react';
import type Guacamole from 'guacamole-common-js';
import { useClipboardBridge, type ClipboardClient } from './useClipboardBridge';

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  // Tests that need the async Clipboard API stub it per-test.
  delete (navigator as { clipboard?: unknown }).clipboard;
});

/** Fake guac input stream; the REAL StringReader decodes its blobs. */
function inputStream() {
  return {
    onblob: null,
    onend: null,
    sendAck: vi.fn(),
  } as unknown as InstanceType<typeof Guacamole.InputStream> & {
    onblob: ((data64: string) => void) | null;
    onend: (() => void) | null;
    sendAck: ReturnType<typeof vi.fn>;
  };
}

function fakeClient() {
  const out = { sendBlob: vi.fn(), sendEnd: vi.fn() };
  const client: ClipboardClient = {
    onclipboard: null,
    createClipboardStream: vi.fn(
      () => out as unknown as InstanceType<typeof Guacamole.OutputStream>,
    ),
  };
  return { client, out, createStream: client.createClipboardStream as ReturnType<typeof vi.fn> };
}

/** Decode everything the writer pushed on the fake output stream. */
function sentText(out: { sendBlob: ReturnType<typeof vi.fn> }): string {
  return out.sendBlob.mock.calls.map((c) => atob(c[0] as string)).join('');
}

/** Drive the desktop→browser path: one text/plain stream carrying `text`. */
function receiveFromDesktop(client: ClipboardClient, text: string) {
  const stream = inputStream();
  client.onclipboard?.(stream, 'text/plain');
  stream.onblob?.(btoa(text));
  stream.onend?.();
}

describe('useClipboardBridge', () => {
  it('desktop → browser: received text is exposed via readRemoteClipboard', () => {
    const { result } = renderHook(() => useClipboardBridge());
    const { client } = fakeClient();
    result.current.attach(client, document.createElement('div'));

    receiveFromDesktop(client, 'hello');
    expect(result.current.readRemoteClipboard()).toBe('hello');
  });

  it('refuses non-text clipboards with an ack and relays nothing', () => {
    const { result } = renderHook(() => useClipboardBridge());
    const { client } = fakeClient();
    result.current.attach(client, document.createElement('div'));

    const stream = inputStream();
    client.onclipboard?.(stream, 'image/png');
    expect(stream.sendAck).toHaveBeenCalledWith('unsupported clipboard type', 0x0100);
    expect(result.current.readRemoteClipboard()).toBe('');
  });

  it('browser → desktop: sendClipboard writes a text/plain stream', () => {
    const { result } = renderHook(() => useClipboardBridge());
    const { client, out, createStream } = fakeClient();
    result.current.attach(client, document.createElement('div'));

    result.current.sendClipboard('hello', true);
    expect(createStream).toHaveBeenCalledWith('text/plain');
    expect(sentText(out)).toBe('hello');
    expect(out.sendEnd).toHaveBeenCalled();
  });

  it('echo guard: text just received from the desktop is not sent back unforced', () => {
    const { result } = renderHook(() => useClipboardBridge());
    const { client, createStream } = fakeClient();
    result.current.attach(client, document.createElement('div'));

    receiveFromDesktop(client, 'copied');
    result.current.sendClipboard('copied');
    expect(createStream).not.toHaveBeenCalled();

    result.current.sendClipboard('copied', true);
    expect(createStream).toHaveBeenCalledTimes(1);
  });

  it('resend guard: the same text is not sent twice unforced', () => {
    const { result } = renderHook(() => useClipboardBridge());
    const { client, createStream } = fakeClient();
    result.current.attach(client, document.createElement('div'));

    result.current.sendClipboard('x', true);
    result.current.sendClipboard('x');
    expect(createStream).toHaveBeenCalledTimes(1);
  });

  it('paste on the container force-sends the pasted text', () => {
    const { result } = renderHook(() => useClipboardBridge());
    const { client, out } = fakeClient();
    const container = document.createElement('div');
    result.current.attach(client, container);

    const event = new Event('paste', { bubbles: true });
    Object.defineProperty(event, 'clipboardData', {
      value: { getData: () => 'pasted' },
    });
    container.dispatchEvent(event);
    expect(sentText(out)).toBe('pasted');
  });

  it('focus re-sync reads the system clipboard through the echo guard', async () => {
    Object.defineProperty(navigator, 'clipboard', {
      value: { readText: () => Promise.resolve('from-system') },
      configurable: true,
    });
    vi.spyOn(document, 'hasFocus').mockReturnValue(true);

    const { result } = renderHook(() => useClipboardBridge());
    const { client, out, createStream } = fakeClient();
    result.current.attach(client, document.createElement('div'));

    await act(async () => {
      window.dispatchEvent(new Event('focus'));
      await Promise.resolve();
    });
    expect(sentText(out)).toBe('from-system');

    // The desktop now holds it: a second focus must NOT re-send (echo).
    createStream.mockClear();
    await act(async () => {
      window.dispatchEvent(new Event('focus'));
      await Promise.resolve();
    });
    expect(createStream).not.toHaveBeenCalled();
  });

  it('detach unbinds everything but keeps the guard state for the next attach', () => {
    const { result } = renderHook(() => useClipboardBridge());
    const first = fakeClient();
    const container = document.createElement('div');
    result.current.attach(first.client, container);
    receiveFromDesktop(first.client, 'kept');

    result.current.detach();
    expect(first.client.onclipboard).toBeNull();
    result.current.sendClipboard('anything', true);
    expect(first.createStream).not.toHaveBeenCalled();
    // Received state survives the reconnect...
    expect(result.current.readRemoteClipboard()).toBe('kept');

    // ...and still feeds the echo guard on the NEXT client.
    const second = fakeClient();
    result.current.attach(second.client, container);
    result.current.sendClipboard('kept');
    expect(second.createStream).not.toHaveBeenCalled();
  });

  it('unmount detaches the listeners', () => {
    const { result, unmount } = renderHook(() => useClipboardBridge());
    const { client, createStream } = fakeClient();
    const container = document.createElement('div');
    result.current.attach(client, container);
    const send = result.current.sendClipboard;

    unmount();
    const event = new Event('paste', { bubbles: true });
    Object.defineProperty(event, 'clipboardData', { value: { getData: () => 'late' } });
    container.dispatchEvent(event);
    send('late', true);
    expect(createStream).not.toHaveBeenCalled();
  });
});
