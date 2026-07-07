// Minimal typings for guacamole-common-js (the package ships no types).
declare module 'guacamole-common-js' {
  namespace Guacamole {
    class WebSocketTunnel {
      constructor(url: string);
      /** Sends one instruction; first element '' is the tunnel-internal
       * opcode (used for the WaaS clipboard controls). */
      sendMessage(...elements: (string | number)[]): void;
    }

    interface Status {
      code: number;
      message?: string;
    }

    class Display {
      getElement(): HTMLElement;
      scale(scale: number): void;
      getScale(): number;
      getWidth(): number;
      getHeight(): number;
    }

    class InputStream {
      onblob: ((data64: string) => void) | null;
      onend: (() => void) | null;
      sendAck(message: string, code: number): void;
    }

    class OutputStream {
      sendBlob(data64: string): void;
      sendEnd(): void;
    }

    class StringReader {
      constructor(stream: InputStream);
      ontext: ((text: string) => void) | null;
      onend: (() => void) | null;
    }

    class StringWriter {
      constructor(stream: OutputStream);
      sendText(text: string): void;
      sendEnd(): void;
    }

    class Client {
      constructor(tunnel: WebSocketTunnel);
      connect(data?: string): void;
      disconnect(): void;
      getDisplay(): Display;
      sendMouseState(state: Mouse.State): void;
      sendKeyEvent(pressed: number, keysym: number): void;
      createClipboardStream(mimetype: string): OutputStream;
      onstatechange: ((state: number) => void) | null;
      onerror: ((status: Status) => void) | null;
      onclipboard: ((stream: InputStream, mimetype: string) => void) | null;
    }

    class Mouse {
      constructor(element: HTMLElement);
      onmousedown: ((state: Mouse.State) => void) | null;
      onmouseup: ((state: Mouse.State) => void) | null;
      onmousemove: ((state: Mouse.State) => void) | null;
    }

    namespace Mouse {
      class State {
        constructor(
          x: number,
          y: number,
          left: boolean,
          middle: boolean,
          right: boolean,
          up: boolean,
          down: boolean,
        );
        x: number;
        y: number;
        left: boolean;
        middle: boolean;
        right: boolean;
        up: boolean;
        down: boolean;
      }
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
