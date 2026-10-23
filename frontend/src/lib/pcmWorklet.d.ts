// Types of the plain-JS worklet module (pcmWorklet.js), for its tests.
export class PcmQueue {
  constructor(channels: number, maxFrames: number);
  readonly frames: number;
  push(samples: Float32Array): void;
  pull(outputs: Float32Array[], frames: number): number;
}
