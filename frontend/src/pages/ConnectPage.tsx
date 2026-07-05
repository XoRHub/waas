import { useEffect, useRef, useState } from 'react';
import { useNavigate, useParams } from 'react-router';
import { useTranslation } from 'react-i18next';
import Guacamole from 'guacamole-common-js';
import { api } from '@/lib/api';
import type { ConnectResult } from '@/types';

type ConnectionState = 'connecting' | 'connected' | 'disconnected' | 'failed';

// Guacamole.Client state 3 = CONNECTED, 5 = DISCONNECTED.
const STATE_CONNECTED = 3;
const STATE_DISCONNECTED = 5;

export function ConnectPage() {
  const { t } = useTranslation();
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const displayRef = useRef<HTMLDivElement>(null);
  const clientRef = useRef<InstanceType<typeof Guacamole.Client> | null>(null);
  const [state, setState] = useState<ConnectionState>('connecting');

  // Back to the portal: close the tab when the portal opened us in a new
  // one (window.close is only honored for script-opened windows), else
  // navigate back in place.
  const leave = () => {
    clientRef.current?.disconnect();
    if (window.opener) {
      window.close();
    }
    navigate('/');
  };

  useEffect(() => {
    if (!id || !displayRef.current) {
      return;
    }
    const container = displayRef.current;
    let client: InstanceType<typeof Guacamole.Client> | null = null;
    let keyboard: InstanceType<typeof Guacamole.Keyboard> | null = null;
    let cancelled = false;

    const connect = async () => {
      let result: ConnectResult;
      try {
        const response = await api.post<ConnectResult>(`/api/v1/workspaces/${id}/connect`);
        result = response.data;
      } catch {
        if (!cancelled) {
          setState('failed');
        }
        return;
      }
      if (cancelled) {
        return;
      }

      const params = new URLSearchParams({
        token: result.connectionToken,
        width: String(container.clientWidth || window.innerWidth),
        height: String(container.clientHeight || window.innerHeight),
        dpi: '96',
      });
      const tunnel = new Guacamole.WebSocketTunnel(`/ws?${params.toString()}`);
      client = new Guacamole.Client(tunnel);
      clientRef.current = client;

      client.onstatechange = (clientState) => {
        if (clientState === STATE_CONNECTED) {
          setState('connected');
        }
        if (clientState === STATE_DISCONNECTED) {
          setState('disconnected');
        }
      };
      client.onerror = () => setState('failed');

      container.appendChild(client.getDisplay().getElement());
      client.connect();

      const mouse = new Guacamole.Mouse(client.getDisplay().getElement());
      const sendMouse = (mouseState: unknown) => client?.sendMouseState(mouseState);
      mouse.onmousedown = sendMouse;
      mouse.onmouseup = sendMouse;
      mouse.onmousemove = sendMouse;

      keyboard = new Guacamole.Keyboard(document);
      keyboard.onkeydown = (keysym) => client?.sendKeyEvent(1, keysym);
      keyboard.onkeyup = (keysym) => client?.sendKeyEvent(0, keysym);
    };

    void connect();

    return () => {
      cancelled = true;
      if (keyboard) {
        keyboard.onkeydown = null;
        keyboard.onkeyup = null;
      }
      client?.disconnect();
      clientRef.current = null;
      container.replaceChildren();
    };
  }, [id]);

  return (
    <div className="flex h-screen flex-col bg-black">
      {state === 'connected' && (
        <div className="group absolute inset-x-0 top-0 z-10 flex justify-center">
          {/* Grab handle: keeps the bar out of the way until hovered. */}
          <div className="absolute top-0 h-2 w-40 rounded-b-md bg-white/20 transition group-hover:opacity-0" />
          <div className="-translate-y-full rounded-b-lg bg-slate-900/90 px-4 py-2 text-sm text-white shadow-lg backdrop-blur transition-transform duration-200 group-hover:translate-y-0">
            <button onClick={leave} className="font-medium text-blue-400 hover:text-blue-300">
              {t('connect.leave')}
            </button>
          </div>
        </div>
      )}
      {state !== 'connected' && (
        <div className="flex flex-1 flex-col items-center justify-center gap-4 text-white">
          <p>
            {state === 'connecting' && t('connect.connecting')}
            {state === 'disconnected' && t('connect.disconnected')}
            {state === 'failed' && t('connect.failed')}
          </p>
          {state !== 'connecting' && (
            <button
              onClick={() => navigate('/')}
              className="rounded-md bg-blue-600 px-4 py-2 text-sm font-medium hover:bg-blue-700"
            >
              {t('connect.back')}
            </button>
          )}
        </div>
      )}
      <div
        ref={displayRef}
        className={state === 'connected' ? 'flex-1 overflow-hidden' : 'hidden'}
      />
    </div>
  );
}
