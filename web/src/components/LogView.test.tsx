import { act, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, expect, it, vi } from 'vitest';
import { FakeEventSource, renderWithProviders } from '../test/render';
import { LogView } from './LogView';

beforeEach(() => {
  FakeEventSource.instances = [];
  vi.stubGlobal('EventSource', FakeEventSource);
  // Deliver batched lines right away.
  vi.stubGlobal('requestAnimationFrame', (cb: FrameRequestCallback) => {
    cb(0);
    return 1;
  });
});

let n = 0;
function log(service: string, line: string, stream = 'stdout') {
  return JSON.stringify({
    time: new Date(Date.UTC(2026, 10, 12, 12, 0, n++)).toISOString(),
    service,
    container: `app-${service}-1`,
    stream,
    line,
  });
}

function renderLogs(props: Partial<Parameters<typeof LogView>[0]> = {}) {
  const onContainerChange = vi.fn();
  renderWithProviders(
    <LogView
      stack="app"
      services={['web', 'db']}
      onContainerChange={onContainerChange}
      {...props}
    />,
  );
  const source = FakeEventSource.instances[0]!;
  return { source, onContainerChange };
}

/** The text of every shown line (the text is split into styled spans). */
function shownLines(): string[] {
  return Array.from(
    screen
      .getByTestId('log-lines')
      .querySelectorAll('div[data-stream] > span:last-child'),
  ).map((el) => el.textContent ?? '');
}

it('shows past and live lines of all services', () => {
  const { source } = renderLogs();
  expect(source.url).toBe('/api/v1/stacks/app/logs?tail=200');
  act(() => {
    source.onopen?.();
    source.emit('log', log('web', 'GET / 200'));
    source.emit('log', log('db', 'ready', 'stderr'));
  });
  expect(shownLines()).toEqual(['GET / 200', 'ready']);
  expect(screen.getByText('2 lines')).toBeInTheDocument();
  // stderr lines are marked.
  expect(
    screen.getByTestId('log-lines').querySelector('[data-stream="stderr"]'),
  ).toHaveTextContent('ready');

  act(() => source.emit('log', log('web', 'GET /health 200')));
  expect(shownLines()).toHaveLength(3);
});

it('renders ANSI colors instead of escape codes', () => {
  const { source } = renderLogs();
  act(() => source.emit('log', log('web', '\x1b[31merror\x1b[0m done')));
  const lines = screen.getByTestId('log-lines');
  expect(lines).toHaveTextContent('error done');
  expect(lines.textContent).not.toContain('\x1b');
  expect(within(lines).getByText('error')).toHaveStyle({ color: '#f2777a' });
});

it('filters by service and by search text', async () => {
  const { source } = renderLogs();
  act(() => {
    source.emit('log', log('web', 'GET /a'));
    source.emit('log', log('db', 'checkpoint'));
    source.emit('log', log('web', 'GET /b'));
  });

  await userEvent.type(screen.getByLabelText('Search…'), '/b');
  expect(shownLines()).toEqual(['GET /b']);
  expect(screen.getByText('/b', { selector: 'mark' })).toBeInTheDocument();

  await userEvent.clear(screen.getByLabelText('Search…'));
  await userEvent.click(screen.getByLabelText('Services'));
  await userEvent.click(screen.getByRole('option', { name: 'db' }));
  await userEvent.keyboard('{Escape}');
  expect(shownLines()).toEqual(['checkpoint']);
});

it('filters by container and can clear that filter', async () => {
  const { source, onContainerChange } = renderLogs({ container: 'app-db-1' });
  act(() => {
    source.emit('log', log('web', 'from web'));
    source.emit('log', log('db', 'from db'));
  });
  expect(shownLines()).toEqual(['from db']);
  await userEvent.click(
    within(screen.getByText('Container: app-db-1').parentElement!).getByTestId(
      'CancelIcon',
    ),
  );
  expect(onContainerChange).toHaveBeenCalledWith(undefined);
});

it('keeps at most maxLines lines', () => {
  const { source } = renderLogs({ maxLines: 3 });
  act(() => {
    for (const i of [1, 2, 3, 4, 5]) source.emit('log', log('web', `l${i}`));
  });
  expect(shownLines()).toEqual(['l3', 'l4', 'l5']);
});

it('starts over after reconnecting and reports failures', () => {
  const { source } = renderLogs();
  act(() => {
    source.onopen?.();
    source.emit('log', log('web', 'one'));
    source.onerror?.();
  });
  expect(screen.getByText(/reconnecting/)).toBeInTheDocument();
  act(() => {
    source.onopen?.(); // the agent sends the past lines again
    source.emit('log', log('web', 'one'));
  });
  expect(shownLines()).toEqual(['one']);

  act(() =>
    source.emit('failed', JSON.stringify({ error: 'docker unavailable' })),
  );
  expect(
    screen.getByText("Can't read the logs: docker unavailable"),
  ).toBeInTheDocument();
});

it('reconnects with a different number of past lines', async () => {
  renderLogs();
  await userEvent.click(screen.getByLabelText('Past lines'));
  await userEvent.click(
    screen.getByRole('option', { name: 'Last 2000 lines' }),
  );
  expect(FakeEventSource.instances.at(-1)!.url).toBe(
    '/api/v1/stacks/app/logs?tail=2000',
  );
});
