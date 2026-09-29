import { useEffect, useReducer } from "react";
import { openInfraLog } from "../api/client";
import { initialLogState, logReducer, type LogState } from "../lib/infra";

/**
 * Log en vivo de un run. Arranca con `seed` (log_tail) hasta que el stream
 * abre; al abrir, el servidor reenvía todo lo guardado y sigue en vivo.
 * `active` reabre el stream cuando el run vuelve a correr (reintento o destroy).
 * `onStatus` avisa cambios de estado y `onDone` el cierre del run.
 */
export function useInfraLog(
  org: string,
  runId: string,
  seed: string[],
  active: boolean,
  cb: { onStatus?: (status: string) => void; onDone?: () => void } = {},
): LogState & { connected: boolean } {
  const [state, dispatch] = useReducer(logReducer, seed, (s) => initialLogState(s));
  const [connected, setConnected] = useReducer((_: boolean, v: boolean) => v, false);
  const { onStatus, onDone } = cb;

  useEffect(() => {
    dispatch({ type: "reset", seed });
    setConnected(false);
    const close = openInfraLog(org, runId, {
      onOpen: () => {
        dispatch({ type: "reset" });
        setConnected(true);
      },
      onError: () => setConnected(false),
      onEvent: (ev) => {
        dispatch(ev);
        if (ev.type === "status") onStatus?.(ev.status);
        if (ev.type === "done") onDone?.();
      },
    });
    return close;
    // seed solo se usa como contenido inicial; no debe reabrir el stream.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [org, runId, active]);

  return { ...state, connected };
}
