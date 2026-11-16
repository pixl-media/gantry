import { act, screen, waitFor } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import {
  FakeEventSource,
  json,
  mockFetch,
  renderWithProviders,
} from '../test/render';
import {
  LiveProvider,
  livePollInterval,
  pollInterval,
  useRefreshInterval,
} from './live';
import { useStack, useStacks } from './queries';

beforeEach(() => {
  FakeEventSource.instances = [];
  vi.stubGlobal('EventSource', FakeEventSource);
});

function Probe() {
  useStacks();
  useStack('app');
  useStack('other');
  return <div>interval {useRefreshInterval()}</div>;
}

/** Counts the requests per path. */
function fetchCounts(fetch: ReturnType<typeof mockFetch>) {
  const counts: Record<string, number> = {};
  for (const r of fetch.requests) {
    const path = new URL(r.url).pathname;
    counts[path] = (counts[path] ?? 0) + 1;
  }
  return counts;
}

function setup() {
  const fetch = mockFetch((request) => {
    const path = new URL(request.url).pathname;
    return path === '/api/v1/stacks'
      ? json({ stacks: [], dockerAvailable: true, warnings: [] })
      : json({ error: 'not found', code: 'not_found' }, 404);
  });
  renderWithProviders(
    <LiveProvider>
      <Probe />
    </LiveProvider>,
  );
  const source = FakeEventSource.instances[0]!;
  expect(source.url).toBe('/api/v1/events');
  return { fetch, source };
}

it('polls slowly while live updates arrive, quickly otherwise', async () => {
  const { source } = setup();
  expect(screen.getByText(`interval ${pollInterval}`)).toBeInTheDocument();

  act(() => source.emit('ready', '{}'));
  expect(
    await screen.findByText(`interval ${livePollInterval}`),
  ).toBeInTheDocument();

  act(() => source.onerror?.());
  expect(
    await screen.findByText(`interval ${pollInterval}`),
  ).toBeInTheDocument();
});

it('reloads only the stacks that changed', async () => {
  const { fetch, source } = setup();
  await waitFor(() =>
    expect(fetchCounts(fetch)).toEqual({
      '/api/v1/stacks': 1,
      '/api/v1/stacks/app': 1,
      '/api/v1/stacks/other': 1,
    }),
  );

  act(() =>
    source.emit('changed', JSON.stringify({ stacks: ['app'], all: false })),
  );
  await waitFor(() =>
    expect(fetchCounts(fetch)).toEqual({
      '/api/v1/stacks': 2,
      '/api/v1/stacks/app': 2,
      '/api/v1/stacks/other': 1,
    }),
  );

  act(() => source.emit('changed', JSON.stringify({ stacks: [], all: true })));
  await waitFor(() =>
    expect(fetchCounts(fetch)).toEqual({
      '/api/v1/stacks': 3,
      '/api/v1/stacks/app': 3,
      '/api/v1/stacks/other': 2,
    }),
  );
});

it('closes the stream when the app layout goes away', () => {
  mockFetch(() => json({}, 404));
  const { unmount } = renderWithProviders(
    <LiveProvider>
      <div />
    </LiveProvider>,
  );
  const source = FakeEventSource.instances[0]!;
  unmount();
  expect(source.readyState).toBe(FakeEventSource.CLOSED);
});
