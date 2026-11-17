import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

interface Entry {
  name: string;
  dir: boolean;
  size: number;
  mtime: string;
}

// Routes wwt serves under /kasm/{sid}/waas/ and relays to the kasm-agent
// sidecar, each gated on the connection token's file-transfer grant.
// Plain fetch, not the api client: the body is the agent's own JSON (no
// envelope), and a 401 here — the session cookie expired — must not sign
// the user out of the portal the way an api-server 401 does.
const filesUrl = (sessionId: string) => `/kasm/${encodeURIComponent(sessionId)}/waas/files`;

const join = (dir: string, name: string) => (dir ? `${dir}/${name}` : name);

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  const units = ['KiB', 'MiB', 'GiB', 'TiB'];
  let v = bytes / 1024;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v < 10 ? Number(v.toFixed(1)) : Math.round(v)} ${units[i]}`;
}

/**
 * Files section of the kasmvnc session menu: browse the workspace home,
 * download files, upload into Uploads — exactly what the user's policy
 * grants (the overlay only mounts it with at least one direction; wwt
 * enforces each request regardless of what is rendered).
 */
export function SessionFiles({
  sessionId,
  canUpload,
  canDownload,
}: {
  sessionId: string;
  canUpload: boolean;
  canDownload: boolean;
}) {
  const { t } = useTranslation();
  const [dir, setDir] = useState('');
  const [entries, setEntries] = useState<Entry[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [status, setStatus] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  // Bumped after an upload so the listing shows the new files.
  const [generation, setGeneration] = useState(0);

  const errorText = (status: unknown) => {
    switch (status) {
      case 403:
        return t('overlay.deniedByPolicy');
      case 413:
        return t('overlay.filesTooLarge');
      case 503:
        return t('overlay.filesUnavailable');
      default:
        return t('overlay.filesError');
    }
  };

  useEffect(() => {
    let cancelled = false;
    fetch(`${filesUrl(sessionId)}?path=${encodeURIComponent(dir)}`, { credentials: 'same-origin' })
      .then(async (r) => {
        if (!r.ok) throw r.status;
        return (await r.json()) as Entry[];
      })
      .then((list) => {
        if (cancelled) return;
        setEntries(list);
        setError(null);
      })
      .catch((e: unknown) => {
        if (cancelled) return;
        setEntries(null);
        setError(errorText(e));
      });
    return () => {
      cancelled = true;
    };
    // errorText only reads t: re-running on language change is noise.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessionId, dir, generation]);

  const upload = async (files: FileList | null) => {
    if (!files || files.length === 0) return;
    const form = new FormData();
    for (const f of Array.from(files)) form.append('files', f, f.name);
    setBusy(true);
    setStatus(null);
    try {
      const r = await fetch(`${filesUrl(sessionId)}/upload`, {
        method: 'POST',
        body: form,
        credentials: 'same-origin',
      });
      if (!r.ok) throw r.status;
      const { stored } = (await r.json()) as { stored: string[] };
      setStatus(t('overlay.filesUploaded', { count: stored.length }));
      // Uploads land in ~/Uploads (the agent's default): show them.
      setDir('Uploads');
      setGeneration((g) => g + 1);
    } catch (e: unknown) {
      setStatus(errorText(e));
    } finally {
      setBusy(false);
    }
  };

  const segments = dir ? dir.split('/') : [];

  return (
    <div className="space-y-2">
      {/* Breadcrumb: Home / a / b, each a jump. */}
      <div className="flex flex-wrap items-center gap-1 text-xs">
        <button
          onClick={() => setDir('')}
          className={segments.length ? 'hover:underline' : 'font-medium text-white'}
        >
          {t('overlay.filesHome')}
        </button>
        {segments.map((s, i) => (
          <span key={i} className="flex items-center gap-1">
            <span className="text-slate-500">/</span>
            <button
              onClick={() => setDir(segments.slice(0, i + 1).join('/'))}
              className={i === segments.length - 1 ? 'font-medium text-white' : 'hover:underline'}
            >
              {s}
            </button>
          </span>
        ))}
      </div>

      {error ? (
        <p className="text-[11px] text-amber-300/90">{error}</p>
      ) : entries === null ? (
        <p className="text-[11px] text-slate-500">…</p>
      ) : entries.length === 0 ? (
        <p className="text-[11px] text-slate-500">{t('overlay.filesEmpty')}</p>
      ) : (
        <ul className="max-h-48 divide-y divide-slate-700/60 overflow-y-auto rounded-md bg-slate-800/60 text-xs">
          {entries.map((e) => (
            <li key={e.name} className="flex items-center justify-between gap-2 px-3 py-1">
              {e.dir ? (
                <button
                  onClick={() => setDir(join(dir, e.name))}
                  className="truncate text-left hover:underline"
                >
                  📁 {e.name}
                </button>
              ) : canDownload ? (
                <a
                  href={`${filesUrl(sessionId)}/download?path=${encodeURIComponent(join(dir, e.name))}`}
                  download={e.name}
                  className="truncate hover:underline"
                >
                  {e.name}
                </a>
              ) : (
                <span className="truncate text-slate-400" title={t('overlay.deniedByPolicy')}>
                  {e.name} 🔒
                </span>
              )}
              {!e.dir && <span className="shrink-0 text-slate-500">{formatSize(e.size)}</span>}
            </li>
          ))}
        </ul>
      )}

      {canUpload && (
        <label className="block w-full cursor-pointer rounded-md bg-slate-700/70 px-3 py-1.5 text-left hover:bg-slate-600 aria-disabled:cursor-not-allowed aria-disabled:opacity-40">
          <input
            type="file"
            multiple
            className="hidden"
            disabled={busy}
            onChange={(e) => {
              void upload(e.target.files);
              e.target.value = '';
            }}
          />
          {busy ? t('overlay.filesUploading') : t('overlay.filesUpload')}
        </label>
      )}
      {status && <p className="text-[11px] text-slate-400">{status}</p>}
      <p className="text-[11px] text-slate-500">
        {canUpload && canDownload
          ? t('overlay.filesHint')
          : canUpload
            ? t('overlay.filesHintUploadOnly')
            : t('overlay.filesHintDownloadOnly')}
      </p>
    </div>
  );
}
