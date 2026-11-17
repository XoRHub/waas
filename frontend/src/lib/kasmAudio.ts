import workletUrl from './pcmWorklet.js?url&no-inline';
import { refreshKasmCookie } from '@/lib/kasmCookie';

// Audio of kasmvnc sessions, through the kasm-agent sidecar's WebSockets
// (wwt routes /kasm/{sid}/waas/audio and /waas/mic, each gated on the
// connection token's audio grant). Wire format: raw PCM s16le 48 kHz in
// 20 ms binary frames — stereo out of the workspace, mono into it.
// ponytail: raw PCM; Opus when the ≈1.5 Mbit/s matter.

export const AUDIO_RATE = 48000;

/** Interleaved s16le bytes → Float32 in [-1, 1). */
export function int16ToFloat32(bytes: ArrayBuffer): Float32Array {
  const pcm = new Int16Array(bytes, 0, bytes.byteLength >> 1);
  const out = new Float32Array(pcm.length);
  for (let i = 0; i < pcm.length; i++) out[i] = pcm[i] / 32768;
  return out;
}

/** Float32 → s16le, clamped. */
export function float32ToInt16(samples: Float32Array): Int16Array {
  const out = new Int16Array(samples.length);
  for (let i = 0; i < samples.length; i++) {
    out[i] = Math.max(-32768, Math.min(32767, Math.round(samples[i] * 32768)));
  }
  return out;
}

/**
 * Linear resampling of interleaved samples; only used when the browser
 * refused a 48 kHz AudioContext (its own rate then differs from the wire).
 */
export function resampleLinear(
  samples: Float32Array,
  from: number,
  to: number,
  channels = 1,
): Float32Array {
  if (from === to) return samples;
  const inFrames = samples.length / channels;
  const outFrames = Math.round((inFrames * to) / from);
  const out = new Float32Array(outFrames * channels);
  for (let f = 0; f < outFrames; f++) {
    const pos = (f * from) / to;
    const i0 = Math.min(Math.floor(pos), inFrames - 1);
    const i1 = Math.min(i0 + 1, inFrames - 1);
    const t = pos - i0;
    for (let c = 0; c < channels; c++) {
      out[f * channels + c] = samples[i0 * channels + c] * (1 - t) + samples[i1 * channels + c] * t;
    }
  }
  return out;
}

export interface AudioLink {
  stop(): void;
}

/** Why a link ended on its own: handshake refused (grant, sidecar down) or closed later. */
export type AudioEnd = 'unavailable' | 'closed';

const wsUrl = (sessionId: string, route: 'audio' | 'mic') =>
  `${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}/kasm/${encodeURIComponent(sessionId)}/waas/${route}`;

const closeQuietly = (ctx: AudioContext) => void ctx.close().catch(() => undefined);

// One context per link: independent lifecycles, nothing shared to tear
// down in the right order. A context whose worklet failed to load is
// closed, not leaked.
async function newContext(): Promise<AudioContext> {
  const ctx = new AudioContext({ sampleRate: AUDIO_RATE });
  try {
    await ctx.audioWorklet.addModule(workletUrl);
  } catch (e) {
    closeQuietly(ctx);
    throw e;
  }
  return ctx;
}

/** startMicrophone's rejection when the browser refused the capture itself. */
export class CaptureRefused extends Error {
  constructor(cause: unknown) {
    super('microphone capture refused', { cause });
  }
}

/**
 * Plays the workspace's sound. The socket rides the session cookie, so it
 * is re-set from a fresh token first (best effort, like the Files
 * section). `onEnd` fires when the stream ends on its own, never on stop().
 */
export async function startPlayback(
  workspaceId: string,
  sessionId: string,
  onEnd: (reason: AudioEnd) => void,
): Promise<AudioLink> {
  await refreshKasmCookie(workspaceId, sessionId).catch(() => undefined);
  const ctx = await newContext();
  const node = new AudioWorkletNode(ctx, 'waas-pcm-player', { outputChannelCount: [2] });
  node.connect(ctx.destination);
  const ws = new WebSocket(wsUrl(sessionId, 'audio'));
  ws.binaryType = 'arraybuffer';
  let opened = false;
  ws.onopen = () => {
    opened = true;
  };
  ws.onmessage = (e: MessageEvent<unknown>) => {
    if (!(e.data instanceof ArrayBuffer)) return;
    const pcm = resampleLinear(int16ToFloat32(e.data), AUDIO_RATE, ctx.sampleRate, 2);
    node.port.postMessage(pcm, [pcm.buffer]);
  };
  ws.onclose = () => {
    closeQuietly(ctx);
    onEnd(opened ? 'closed' : 'unavailable');
  };
  // The KasmVNC client posts enable_audio to its parent on every click in
  // the desktop — a user gesture, which lets a context the browser kept
  // suspended (autoplay policy) start.
  const onGesture = (e: MessageEvent) => {
    if (e.origin !== location.origin || ctx.state !== 'suspended') return;
    if ((e.data as { action?: string } | null)?.action === 'enable_audio') void ctx.resume();
  };
  window.addEventListener('message', onGesture);
  return {
    stop() {
      window.removeEventListener('message', onGesture);
      ws.onclose = null;
      ws.close();
      closeQuietly(ctx);
    },
  };
}

/**
 * Sends the browser's microphone into the workspace. Rejects with
 * CaptureRefused (wrapping getUserMedia's error) when the capture is
 * refused — permission, no device, insecure context — and with the setup
 * error otherwise; either way before any socket is opened, and with the
 * capture released: a failed link never leaves the microphone live.
 */
export async function startMicrophone(
  workspaceId: string,
  sessionId: string,
  onEnd: (reason: AudioEnd) => void,
): Promise<AudioLink> {
  const stream = await navigator.mediaDevices
    .getUserMedia({ audio: { channelCount: 1, echoCancellation: true } })
    .catch((e: unknown) => {
      throw new CaptureRefused(e);
    });
  const stopTracks = () => stream.getTracks().forEach((t) => t.stop());
  let ctx: AudioContext | undefined;
  let node: AudioWorkletNode;
  try {
    await refreshKasmCookie(workspaceId, sessionId).catch(() => undefined);
    ctx = await newContext();
    node = new AudioWorkletNode(ctx, 'waas-pcm-capture', { outputChannelCount: [1] });
    ctx.createMediaStreamSource(stream).connect(node);
    // Connected so the graph keeps running; the capture outputs silence.
    node.connect(ctx.destination);
  } catch (e) {
    stopTracks();
    // Unset when newContext itself failed: it closed its own.
    if (ctx) closeQuietly(ctx);
    throw e;
  }
  const ws = new WebSocket(wsUrl(sessionId, 'mic'));
  let opened = false;
  ws.onopen = () => {
    opened = true;
  };
  node.port.onmessage = (e: MessageEvent<Float32Array>) => {
    if (ws.readyState !== WebSocket.OPEN) return;
    ws.send(float32ToInt16(resampleLinear(e.data, ctx.sampleRate, AUDIO_RATE)).buffer);
  };
  ws.onclose = () => {
    stopTracks();
    closeQuietly(ctx);
    onEnd(opened ? 'closed' : 'unavailable');
  };
  return {
    stop() {
      ws.onclose = null;
      ws.close();
      stopTracks();
      closeQuietly(ctx);
    },
  };
}
