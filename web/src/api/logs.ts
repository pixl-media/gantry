import { useEffect, useRef, useState } from 'react';
import type { components } from './schema.gen';

export type LogLine = components['schemas']['LogLine'];

/** A log line with a stable key for rendering. */
export interface KeyedLogLine extends LogLine {
  key: number;
}

export interface StackLogs {
  lines: KeyedLogLine[];
  /** True while the connection is lost and the browser reconnects. */
  reconnecting: boolean;
  /** Set when the agent reported that it can't read the logs. */
  failed?: string;
}

/** The most lines kept in the browser; older ones are dropped. */
export const maxLogLines = 10_000;

/**
 * Follows a stack's logs over Server-Sent Events. Lines are collected and
 * handed to React at most once per animation frame, so chatty containers
 * don't cause a render per line. On every (re)connect the agent sends the
 * past lines again, so the buffer is reset when the connection opens.
 */
export function useStackLogs(
  stack: string,
  tail: number,
  maxLines = maxLogLines,
): StackLogs & { clear: () => void } {
  const [state, setState] = useState<StackLogs>({
    lines: [],
    reconnecting: false,
  });
  const pending = useRef<KeyedLogLine[]>([]);
  const scheduled = useRef(false);
  const frame = useRef<number | undefined>(undefined);
  const nextKey = useRef(0);

  useEffect(() => {
    const flush = () => {
      scheduled.current = false;
      const batch = pending.current;
      pending.current = [];
      setState((s) => {
        const lines = s.lines.concat(batch);
        return {
          ...s,
          lines: lines.length > maxLines ? lines.slice(-maxLines) : lines,
        };
      });
    };
    const source = new EventSource(
      `/api/v1/stacks/${encodeURIComponent(stack)}/logs?tail=${tail}`,
    );
    source.onopen = () => {
      pending.current = [];
      setState({ lines: [], reconnecting: false });
    };
    source.addEventListener('log', (e) => {
      const line = JSON.parse((e as MessageEvent<string>).data) as LogLine;
      pending.current.push({ ...line, key: nextKey.current++ });
      // A flag rather than the frame id: the callback may run before
      // requestAnimationFrame returns its id.
      if (!scheduled.current) {
        scheduled.current = true;
        frame.current = requestAnimationFrame(flush);
      }
    });
    source.addEventListener('failed', (e) => {
      const { error } = JSON.parse((e as MessageEvent<string>).data) as {
        error: string;
      };
      setState((s) => ({ ...s, failed: error }));
      source.close();
    });
    source.onerror = () => {
      if (source.readyState !== EventSource.CLOSED) {
        setState((s) => ({ ...s, reconnecting: true }));
      }
    };
    return () => {
      source.close();
      if (scheduled.current && frame.current !== undefined) {
        cancelAnimationFrame(frame.current);
      }
      scheduled.current = false;
      pending.current = [];
      setState({ lines: [], reconnecting: false });
    };
  }, [stack, tail, maxLines]);

  const clear = () => setState((s) => ({ ...s, lines: [] }));
  return { ...state, clear };
}
