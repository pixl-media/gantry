import { useQueryClient } from '@tanstack/react-query';
import {
  createContext,
  useContext,
  useEffect,
  useState,
  type ReactNode,
} from 'react';
import type { components } from './schema.gen';

type StackChanges = components['schemas']['StackChanges'];

/** How often lists and details refresh without a live connection. */
export const pollInterval = 5000;
/** How often they refresh as a safety net while the live connection works. */
export const livePollInterval = 60_000;
/**
 * Without any event for this long, the live connection counts as dead. The
 * agent pings every 25 seconds.
 */
export const liveTimeout = 70_000;

const LiveContext = createContext(false);

/**
 * Follows `/api/v1/events` and reloads the stack data that changed, so the
 * pages don't have to poll every few seconds. While the stream is down, they
 * poll as before.
 */
export function LiveProvider({ children }: { children: ReactNode }) {
  const queryClient = useQueryClient();
  const [connected, setConnected] = useState(false);

  useEffect(() => {
    const reloadAll = () =>
      Promise.all(
        ['/api/v1/stacks', '/api/v1/stacks/{name}', '/api/v1/operations'].map(
          (path) => queryClient.invalidateQueries({ queryKey: ['get', path] }),
        ),
      );
    const reload = (stacks: string[]) =>
      Promise.all([
        queryClient.invalidateQueries({ queryKey: ['get', '/api/v1/stacks'] }),
        ...stacks.flatMap((name) => [
          queryClient.invalidateQueries({
            queryKey: [
              'get',
              '/api/v1/stacks/{name}',
              { params: { path: { name } } },
            ],
          }),
          queryClient.invalidateQueries({
            queryKey: [
              'get',
              '/api/v1/operations',
              { params: { query: { stack: name } } },
            ],
          }),
        ]),
      ]);

    let source: EventSource | undefined;
    let lastEvent = Date.now();
    const connect = () => {
      source?.close();
      lastEvent = Date.now();
      source = new EventSource('/api/v1/events');
      // Sent on every (re)connect: changes may have been missed meanwhile.
      source.addEventListener('ready', () => {
        lastEvent = Date.now();
        setConnected(true);
        void reloadAll();
      });
      source.addEventListener('changed', (e) => {
        lastEvent = Date.now();
        const c = JSON.parse((e as MessageEvent<string>).data) as StackChanges;
        void (c.all ? reloadAll() : reload(c.stacks));
      });
      source.addEventListener('ping', () => {
        lastEvent = Date.now();
      });
      // EventSource reconnects by itself; poll quickly until it's back.
      source.onerror = () => setConnected(false);
    };
    connect();
    // A connection can die without an error reaching the browser (sleep,
    // network change, a proxy that keeps the client side open). The agent
    // pings regularly, so silence means it's gone: start over.
    const watchdog = setInterval(() => {
      if (Date.now() - lastEvent > liveTimeout) {
        setConnected(false);
        connect();
      }
    }, 10_000);
    return () => {
      clearInterval(watchdog);
      source?.close();
    };
  }, [queryClient]);

  return <LiveContext value={connected}>{children}</LiveContext>;
}

/** The refresh interval for stack data: slow while live updates arrive. */
export function useRefreshInterval(): number {
  return useContext(LiveContext) ? livePollInterval : pollInterval;
}
