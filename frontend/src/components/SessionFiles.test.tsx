// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';
import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@/test/render';
import { createApiMock } from '@/test/apiMock';
import { SessionFiles } from './SessionFiles';

// The token refresh goes through the api client; the files routes do not.
const apiMock = createApiMock();
vi.mock('@/lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/api')>()),
  get api() {
    return apiMock.api;
  },
}));

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });

const home = [
  { name: 'Documents', dir: true, size: 4096, mtime: '2026-10-23T00:00:00Z' },
  { name: 'notes.txt', dir: false, size: 1536, mtime: '2026-10-23T00:00:00Z' },
];
const documents = [
  { name: 'report.pdf', dir: false, size: 5 << 20, mtime: '2026-10-23T00:00:00Z' },
];

// The routes wwt serves under /kasm/{sid}/waas/, by (method, path, query);
// the cookie refresh answers 204 for every session.
function stubFiles(routes: Record<string, () => Response>) {
  const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === 'string' ? input : input.toString();
    if (/^\/kasm\/[^/]+\/waas\/session\?token=tok$/.test(url)) {
      return Promise.resolve(new Response(null, { status: 204 }));
    }
    const key = `${init?.method ?? 'GET'} ${url}`;
    const route = routes[key];
    return Promise.resolve(route ? route() : new Response('nope', { status: 500 }));
  });
  vi.stubGlobal('fetch', fetchMock);
  apiMock.api.post.mockResolvedValue({
    data: { sessionId: 's1', connectionToken: 'tok', protocol: 'kasmvnc' },
  });
  return fetchMock;
}

afterEach(() => {
  vi.unstubAllGlobals();
  apiMock.api.post.mockClear();
});

describe('SessionFiles', () => {
  it('browses the home, follows folders and links downloads to the gated route', async () => {
    const fetchMock = stubFiles({
      'GET /kasm/s1/waas/files?path=': () => json(home),
      'GET /kasm/s1/waas/files?path=Documents': () => json(documents),
    });
    renderWithProviders(
      <SessionFiles workspaceId="w1" sessionId="s1" canUpload={false} canDownload />,
    );

    expect(await screen.findByText('notes.txt')).toHaveAttribute(
      'href',
      '/kasm/s1/waas/files/download?path=notes.txt',
    );
    expect(screen.getByText('1.5 KiB')).toBeInTheDocument();
    // Download only: no upload control.
    expect(screen.queryByText('Upload files…')).toBeNull();
    expect(screen.getByText(/Uploading is denied/)).toBeInTheDocument();
    // The cookie is re-set from a fresh token BEFORE the listing rides it.
    expect(apiMock.api.post).toHaveBeenCalledWith('/api/v1/workspaces/w1/sessions/s1/token');
    expect(fetchMock.mock.calls.map(([url]) => String(url))).toEqual([
      '/kasm/s1/waas/session?token=tok',
      '/kasm/s1/waas/files?path=',
    ]);

    await userEvent.click(screen.getByRole('button', { name: /Documents/ }));
    expect(await screen.findByText('report.pdf')).toHaveAttribute(
      'href',
      '/kasm/s1/waas/files/download?path=Documents%2Freport.pdf',
    );
    expect(screen.getByText('5 MiB')).toBeInTheDocument();
    // The requests ride the session cookie, never the api client.
    expect(fetchMock).toHaveBeenCalledWith(
      '/kasm/s1/waas/files?path=Documents',
      expect.objectContaining({ credentials: 'same-origin' }),
    );

    // Breadcrumb: back home.
    await userEvent.click(screen.getByRole('button', { name: 'Home' }));
    expect(await screen.findByText('notes.txt')).toBeInTheDocument();
  });

  it('uploads into Uploads and shows them; files are not download links without the grant', async () => {
    const uploads = [{ name: 'a.txt', dir: false, size: 3, mtime: '2026-10-23T00:00:00Z' }];
    const fetchMock = stubFiles({
      'GET /kasm/s1/waas/files?path=': () => json(home),
      'GET /kasm/s1/waas/files?path=Uploads': () => json(uploads),
      'POST /kasm/s1/waas/files/upload': () => json({ stored: ['a.txt'] }),
    });
    renderWithProviders(
      <SessionFiles workspaceId="w1" sessionId="s1" canUpload canDownload={false} />,
    );

    // Upload only: the file is listed, locked, not a link.
    expect(await screen.findByText(/notes.txt/)).not.toHaveAttribute('href');
    expect(screen.getByTitle('Denied by your policy')).toBeInTheDocument();

    const input = screen.getByLabelText('Upload files…') as HTMLInputElement;
    await userEvent.upload(input, new File(['abc'], 'a.txt', { type: 'text/plain' }));

    expect(await screen.findByText('1 file uploaded to Uploads')).toBeInTheDocument();
    expect(await screen.findByText(/a.txt/)).toBeInTheDocument();
    const urls = fetchMock.mock.calls.map(([url, init]) => `${init?.method ?? 'GET'} ${url}`);
    const post = urls.indexOf('POST /kasm/s1/waas/files/upload');
    expect(post).toBeGreaterThan(0);
    // A fresh cookie right before the upload too (a large one may outlive
    // the token otherwise).
    expect(urls[post - 1]).toBe('GET /kasm/s1/waas/session?token=tok');
    const postCall = fetchMock.mock.calls[post];
    expect((postCall[1]?.body as FormData).getAll('files')).toHaveLength(1);
  });

  it('names the gate on 403, the cap on 413, the sidecar on 503 and the cookie on 401', async () => {
    stubFiles({
      'GET /kasm/s1/waas/files?path=': () => json(home),
      'POST /kasm/s1/waas/files/upload': () => new Response('too big', { status: 413 }),
    });
    renderWithProviders(<SessionFiles workspaceId="w1" sessionId="s1" canUpload canDownload />);
    await screen.findByText('notes.txt');
    await userEvent.upload(
      screen.getByLabelText('Upload files…'),
      new File(['x'.repeat(10)], 'big.bin'),
    );
    expect(await screen.findByText(/Too large/)).toBeInTheDocument();

    stubFiles({ 'GET /kasm/s2/waas/files?path=': () => new Response('no', { status: 403 }) });
    renderWithProviders(<SessionFiles workspaceId="w1" sessionId="s2" canUpload canDownload />);
    expect(await screen.findByText('Denied by your policy')).toBeInTheDocument();

    stubFiles({ 'GET /kasm/s3/waas/files?path=': () => new Response('no', { status: 503 }) });
    renderWithProviders(<SessionFiles workspaceId="w1" sessionId="s3" canUpload canDownload />);
    await waitFor(() =>
      expect(screen.getByText(/not available on this workspace/)).toBeInTheDocument(),
    );

    // The refresh failed (session ended underneath) and wwt refuses the
    // stale cookie: the listing says so instead of a generic error.
    stubFiles({ 'GET /kasm/s4/waas/files?path=': () => new Response('no', { status: 401 }) });
    apiMock.api.post.mockRejectedValue(new Error('409 session already ended'));
    renderWithProviders(<SessionFiles workspaceId="w1" sessionId="s4" canUpload canDownload />);
    expect(await screen.findByText(/Session expired/)).toBeInTheDocument();
  });
});
