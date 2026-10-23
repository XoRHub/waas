import { describe, expect, it } from 'vitest';
import { float32ToInt16, int16ToFloat32, resampleLinear } from './kasmAudio';
import { PcmQueue } from './pcmWorklet.js';

describe('PCM conversion', () => {
  it('round-trips s16le through float and clamps out-of-range samples', () => {
    const pcm = new Int16Array([0, 1, -1, 32767, -32768, 12345]);
    const f = int16ToFloat32(pcm.buffer);
    expect(f[0]).toBe(0);
    expect(f[3]).toBeCloseTo(0.99997, 4);
    expect(f[4]).toBe(-1);
    expect(Array.from(float32ToInt16(f))).toEqual(Array.from(pcm));
    expect(Array.from(float32ToInt16(new Float32Array([2, -2])))).toEqual([32767, -32768]);
  });

  it('ignores a trailing odd byte', () => {
    expect(int16ToFloat32(new Uint8Array([0, 0, 1]).buffer)).toHaveLength(1);
  });
});

describe('resampleLinear', () => {
  it('returns the input untouched at equal rates', () => {
    const x = new Float32Array([1, 2]);
    expect(resampleLinear(x, 48000, 48000)).toBe(x);
  });

  it('scales the frame count by the rate ratio and interpolates per channel', () => {
    // 44.1 kHz mono → 48 kHz: 441 frames become 480.
    expect(resampleLinear(new Float32Array(441), 44100, 48000)).toHaveLength(480);
    // Stereo ramp halved in rate: frames interleave L/R and sit midway.
    const stereo = new Float32Array([0, 10, 1, 11, 2, 12, 3, 13]);
    const out = resampleLinear(stereo, 48000, 24000, 2);
    expect(Array.from(out)).toEqual([0, 10, 2, 12]);
    const up = resampleLinear(new Float32Array([0, 1]), 24000, 48000);
    expect(Array.from(up)).toEqual([0, 0.5, 1, 1]);
  });
});

describe('PcmQueue (jitter buffer)', () => {
  const pull = (q: PcmQueue, frames: number, channels = 2) => {
    const outs = Array.from({ length: channels }, () => new Float32Array(frames));
    const n = q.pull(outs, frames);
    return { n, outs };
  };

  it('de-interleaves across chunk boundaries and zero-fills an underrun', () => {
    const q = new PcmQueue(2, 1000);
    q.push(new Float32Array([1, -1, 2, -2]));
    q.push(new Float32Array([3, -3]));
    expect(q.frames).toBe(3);
    const { n, outs } = pull(q, 4);
    expect(n).toBe(3);
    expect(Array.from(outs[0])).toEqual([1, 2, 3, 0]);
    expect(Array.from(outs[1])).toEqual([-1, -2, -3, 0]);
    expect(q.frames).toBe(0);
  });

  it('drops the oldest chunks once too far behind the live edge', () => {
    const q = new PcmQueue(1, 4);
    q.push(new Float32Array([1, 2]));
    q.push(new Float32Array([3, 4]));
    q.push(new Float32Array([5, 6]));
    expect(q.frames).toBe(4);
    expect(Array.from(pull(q, 4, 1).outs[0])).toEqual([3, 4, 5, 6]);
  });

  it('never drops the only chunk, even when it exceeds the ceiling', () => {
    const q = new PcmQueue(1, 2);
    q.push(new Float32Array([1, 2, 3]));
    expect(q.frames).toBe(3);
  });
});
