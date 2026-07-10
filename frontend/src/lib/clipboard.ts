// Clipboard bridging between the browser and the remote desktop.
//
// The wire protocol (guac "clipboard" streams) and its policy enforcement
// live in wwt; this module owns the BROWSER side: when to push the local
// clipboard to the desktop, and how to avoid the echo loop that appears
// once both directions are wired (remote copy → local write → focus sync
// → sent back to the remote).
//
// Browser reality the callers must work with:
//  - navigator.clipboard only exists in secure contexts (HTTPS or
//    localhost). Plain-HTTP deployments are limited to the overlay's
//    manual exchange in BOTH directions: the `paste` DOM event is no way
//    out, since Guacamole.Keyboard preventDefaults the Ctrl+V keydown and
//    the native paste action never runs (verified live — the dev env
//    serves HTTPS on :8443 for this reason).
//  - Firefox exposes writeText but not readText: remote→local is
//    seamless, local→remote is the overlay's manual exchange.

/** True when the async Clipboard API is available (secure context). */
export function hasClipboardApi(): boolean {
  return typeof navigator !== 'undefined' && !!navigator.clipboard;
}

/** True when the page can READ the system clipboard (Chromium + HTTPS). */
export function canReadSystemClipboard(): boolean {
  return hasClipboardApi() && typeof navigator.clipboard.readText === 'function';
}

/**
 * Echo/duplicate guard for one desktop session. Directions:
 *  - receive(text): a clipboard update arrived FROM the desktop;
 *  - shouldSend(text): whether a locally observed text is worth sending
 *    (drops empties, resends, and echoes of what the desktop just sent);
 *  - sent(text): record a completed send.
 */
export class ClipboardSync {
  private lastSent = '';
  private received = '';

  receive(text: string) {
    this.received = text;
  }

  /** Last text received from the desktop (overlay manual fallback). */
  get lastReceived(): string {
    return this.received;
  }

  shouldSend(text: string): boolean {
    return text !== '' && text !== this.lastSent && text !== this.received;
  }

  sent(text: string) {
    this.lastSent = text;
  }
}
