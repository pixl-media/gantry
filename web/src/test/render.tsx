import { ThemeProvider } from '@mui/material';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render } from '@testing-library/react';
import type { ReactElement } from 'react';
import { MemoryRouter } from 'react-router';
import { vi } from 'vitest';
import { NotificationProvider } from '../components/Notifications';
import { OperationPanelProvider } from '../components/OperationPanel';
import { theme } from '../theme';

/** Renders ui with the app's providers, at the given route. */
export function renderWithProviders(ui: ReactElement, route = '/') {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <ThemeProvider theme={theme}>
        <MemoryRouter initialEntries={[route]}>
          <NotificationProvider>
            <OperationPanelProvider>{ui}</OperationPanelProvider>
          </NotificationProvider>
        </MemoryRouter>
      </ThemeProvider>
    </QueryClientProvider>,
  );
}

type Handler = (request: Request) => Response | Promise<Response>;

/** Replaces fetch; handler gets every request the app makes. */
export function mockFetch(handler: Handler) {
  const fn = vi.fn((input: RequestInfo | URL, init?: RequestInit) =>
    Promise.resolve(handler(new Request(input, init))),
  );
  vi.stubGlobal('fetch', fn);
  return fn;
}

export function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

/** A stand-in for EventSource that tests drive by hand. */
export class FakeEventSource {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSED = 2;
  static instances: FakeEventSource[] = [];

  readyState = FakeEventSource.OPEN;
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  private listeners = new Map<string, ((e: MessageEvent) => void)[]>();

  constructor(public url: string) {
    FakeEventSource.instances.push(this);
  }

  addEventListener(type: string, fn: (e: MessageEvent) => void) {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), fn]);
  }

  emit(type: string, data: string) {
    for (const fn of this.listeners.get(type) ?? []) {
      fn(new MessageEvent(type, { data }));
    }
  }

  close() {
    this.readyState = FakeEventSource.CLOSED;
  }
}
