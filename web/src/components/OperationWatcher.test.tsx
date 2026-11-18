import { useQueryClient } from '@tanstack/react-query';
import { act, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, expect, it, vi } from 'vitest';
import type { Operation } from '../api/client';
import {
  FakeEventSource,
  json,
  mockFetch,
  renderWithProviders,
} from '../test/render';
import { useOperationPanel } from './OperationPanel';
import { OperationWatcher } from './OperationWatcher';

const op: Operation = {
  id: 'op_1',
  stack: 'whoami',
  action: 'update',
  status: 'running',
  startedAt: '2026-11-18T12:00:00Z',
};

/** A fake agent whose operation ends with `result` once `finish` is called. */
function fakeAgent(result: Operation['status']) {
  let status: Operation['status'] = 'running';
  mockFetch((req) => {
    if (new URL(req.url).pathname === '/api/v1/operations') {
      return json({ operations: [{ ...op, status }] });
    }
    return json({ error: 'unexpected' }, 500);
  });
  return { finish: () => (status = result) };
}

/** Starts the operation as the app does, and refreshes like a live event. */
function Harness({ started }: { started: Operation }) {
  const open = useOperationPanel();
  const queryClient = useQueryClient();
  return (
    <>
      <button onClick={() => open(started)}>start</button>
      <button
        onClick={() =>
          queryClient.invalidateQueries({
            queryKey: ['get', '/api/v1/operations'],
          })
        }
      >
        refresh
      </button>
      <OperationWatcher />
    </>
  );
}

beforeEach(() => {
  FakeEventSource.instances = [];
  vi.stubGlobal('EventSource', FakeEventSource);
});

async function startAndClose(started: Operation = op) {
  renderWithProviders(<Harness started={started} />);
  await userEvent.click(screen.getByRole('button', { name: 'start' }));
  await userEvent.click(await screen.findByRole('button', { name: 'Close' }));
  await waitFor(() =>
    expect(
      screen.queryByRole('button', { name: 'Close' }),
    ).not.toBeInTheDocument(),
  );
}

async function refresh() {
  await act(() =>
    userEvent.click(
      screen.getByRole('button', { name: 'refresh', hidden: true }),
    ),
  );
}

it('reports a finished operation once its panel is closed', async () => {
  const agent = fakeAgent('succeeded');
  await startAndClose();
  agent.finish();
  await refresh();
  expect(await screen.findByText('Update · whoami: done.')).toBeInTheDocument();
});

it('offers the output of a failed operation', async () => {
  const agent = fakeAgent('failed');
  await startAndClose();
  agent.finish();
  await refresh();
  const alert = (await screen.findByText('Update · whoami: failed.')).closest(
    '[role="alert"]',
  ) as HTMLElement;
  await userEvent.click(
    within(alert).getByRole('button', { name: 'Show output', hidden: true }),
  );
  expect(
    await screen.findByRole('button', { name: 'Close' }),
  ).toBeInTheDocument();
  expect(FakeEventSource.instances.at(-1)?.url).toBe(
    '/api/v1/operations/op_1/events',
  );
});

it('stays quiet while the panel shows the operation', async () => {
  const agent = fakeAgent('succeeded');
  renderWithProviders(<Harness started={op} />);
  await userEvent.click(screen.getByRole('button', { name: 'start' }));
  await screen.findByRole('button', { name: 'Close' });
  agent.finish();
  await refresh();
  await waitFor(() =>
    expect(screen.queryByText(/: done\./)).not.toBeInTheDocument(),
  );
});

it("doesn't watch an operation that had ended already", async () => {
  fakeAgent('succeeded').finish();
  await startAndClose({ ...op, status: 'succeeded' });
  await refresh();
  await waitFor(() =>
    expect(screen.queryByText(/: done\./)).not.toBeInTheDocument(),
  );
});
