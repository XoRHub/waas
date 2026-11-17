// AudioWorklet module of the kasm audio link (lib/kasmAudio.ts): a player
// fed interleaved Float32 PCM by the main thread, and a capture posting
// the microphone input back in 20 ms blocks. Plain JS served as a static
// asset (`?url&no-inline`): the worklet scope cannot share the page's
// bundle, and a Blob URL would need `blob:` in the CSP's script-src.
// PcmQueue is exported for the tests; the worklet globals only exist in
// the worklet scope, hence the guard at the bottom.

/**
 * PcmQueue is the jitter buffer: a FIFO of interleaved sample chunks with
 * a drop-oldest ceiling, so a stream running ahead of the clock (network
 * burst after a stall) never builds up latency.
 */
export class PcmQueue {
  constructor(channels, maxFrames) {
    this.channels = channels;
    this.maxSamples = maxFrames * channels;
    this.chunks = [];
    this.head = 0; // read offset into chunks[0]
    this.size = 0; // buffered samples
  }

  get frames() {
    return this.size / this.channels;
  }

  push(samples) {
    this.chunks.push(samples);
    this.size += samples.length;
    while (this.size > this.maxSamples && this.chunks.length > 1) {
      this.size -= this.chunks[0].length - this.head;
      this.chunks.shift();
      this.head = 0;
    }
  }

  /**
   * De-interleaves up to `frames` frames into the per-channel outputs,
   * zero-filling what is missing; returns the frames delivered.
   */
  pull(outputs, frames) {
    let delivered = 0;
    while (delivered < frames && this.chunks.length > 0) {
      const chunk = this.chunks[0];
      const n = Math.min(frames - delivered, (chunk.length - this.head) / this.channels);
      for (let f = 0; f < n; f++) {
        for (let c = 0; c < this.channels; c++) {
          outputs[c][delivered + f] = chunk[this.head + f * this.channels + c];
        }
      }
      this.head += n * this.channels;
      this.size -= n * this.channels;
      delivered += n;
      if (this.head >= chunk.length) {
        this.chunks.shift();
        this.head = 0;
      }
    }
    for (const out of outputs) out.fill(0, delivered, frames);
    return delivered;
  }
}

if (typeof registerProcessor === 'function') {
  // Prime with 60 ms before playing (and again after an underrun), drop
  // beyond 300 ms: the agent sends 20 ms frames.
  const PRIME_FRAMES = sampleRate * 0.06;
  const MAX_FRAMES = sampleRate * 0.3;
  const CAPTURE_FRAMES = Math.round(sampleRate * 0.02);

  class PcmPlayer extends AudioWorkletProcessor {
    constructor() {
      super();
      this.queue = new PcmQueue(2, MAX_FRAMES);
      this.primed = false;
      this.port.onmessage = (e) => this.queue.push(e.data);
    }

    process(_inputs, outputs) {
      const out = outputs[0];
      if (!this.primed) {
        if (this.queue.frames < PRIME_FRAMES) {
          for (const ch of out) ch.fill(0);
          return true;
        }
        this.primed = true;
      }
      if (this.queue.pull(out, out[0].length) < out[0].length) this.primed = false;
      return true;
    }
  }

  class PcmCapture extends AudioWorkletProcessor {
    constructor() {
      super();
      this.buf = new Float32Array(CAPTURE_FRAMES);
      this.fill = 0;
    }

    process(inputs) {
      const ch = inputs[0] && inputs[0][0];
      if (!ch) return true;
      let i = 0;
      while (i < ch.length) {
        const n = Math.min(ch.length - i, CAPTURE_FRAMES - this.fill);
        this.buf.set(ch.subarray(i, i + n), this.fill);
        this.fill += n;
        i += n;
        if (this.fill === CAPTURE_FRAMES) {
          this.port.postMessage(this.buf, [this.buf.buffer]);
          this.buf = new Float32Array(CAPTURE_FRAMES);
          this.fill = 0;
        }
      }
      return true;
    }
  }

  registerProcessor('waas-pcm-player', PcmPlayer);
  registerProcessor('waas-pcm-capture', PcmCapture);
}
