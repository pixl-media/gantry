import { useEffect, useState } from 'react';
import type { Operation } from './client';

export interface OperationEvents {
  /** Output lines so far. */
  lines: string[];
  /** The finished operation, once the stream has ended. */
  final?: Operation;
  /** True while the connection is lost and the browser reconnects. */
  reconnecting: boolean;
}

const initial: OperationEvents = { lines: [], reconnecting: false };

/**
 * Follows an operation's live output over Server-Sent Events. The session
 * cookie authenticates the stream, so the plain EventSource works. On every
 * (re)connect the agent replays the output from the start, so the lines are
 * reset when the connection opens.
 */
export function useOperationEvents(id: string | undefined): OperationEvents {
  const [state, setState] = useState<OperationEvents>(initial);

  useEffect(() => {
    if (!id) return;
    const source = new EventSource(
      `/api/v1/operations/${encodeURIComponent(id)}/events`,
    );
    source.onopen = () => setState(initial);
    source.addEventListener('output', (e) => {
      const line = (e as MessageEvent<string>).data;
      setState((s) => ({ ...s, lines: [...s.lines, line] }));
    });
    source.addEventListener('done', (e) => {
      const final = JSON.parse((e as MessageEvent<string>).data) as Operation;
      setState((s) => ({ ...s, final, reconnecting: false }));
      source.close();
    });
    source.onerror = () => {
      // EventSource reconnects by itself unless the stream is closed.
      if (source.readyState !== EventSource.CLOSED) {
        setState((s) => ({ ...s, reconnecting: true }));
      }
    };
    return () => {
      source.close();
      setState(initial);
    };
  }, [id]);

  return state;
}
