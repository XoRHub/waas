// Minimal typings for guacamole-common-js (the package ships no types).
declare module 'guacamole-common-js' {
  namespace Guacamole {
    class WebSocketTunnel {
      constructor(url: string);
    }

    interface Status {
      code: number;
      message?: string;
    }

    class Display {
      getElement(): HTMLElement;
      scale(scale: number): void;
      getWidth(): number;
      getHeight(): number;
    }

    class Client {
      constructor(tunnel: WebSocketTunnel);
      connect(data?: string): void;
      disconnect(): void;
      getDisplay(): Display;
      sendMouseState(state: unknown): void;
      sendKeyEvent(pressed: number, keysym: number): void;
      onstatechange: ((state: number) => void) | null;
      onerror: ((status: Status) => void) | null;
    }

    class Mouse {
      constructor(element: HTMLElement);
      onmousedown: ((state: unknown) => void) | null;
      onmouseup: ((state: unknown) => void) | null;
      onmousemove: ((state: unknown) => void) | null;
    }

    class Keyboard {
      constructor(element: Document | HTMLElement);
      onkeydown: ((keysym: number) => void) | null;
      onkeyup: ((keysym: number) => void) | null;
      reset(): void;
    }
  }

  export default Guacamole;
}
