import { act, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, expect, it, vi } from 'vitest';
import {
  FakeEventSource,
  json,
  mockFetch,
  renderWithProviders,
} from '../test/render';
import { OperationView } from './OperationPanel';

const target = {
  id: 'op_1',
  stack: 'whoami',
  action: 'update' as const,
  startedAt: '2026-11-12T12:00:00Z',
};

beforeEach(() => {
  FakeEventSource.instances = [];
  vi.stubGlobal('EventSource', FakeEventSource);
});

it('streams output live and shows the result', async () => {
  renderWithProviders(<OperationView target={target} onClose={() => {}} />);
  const source = FakeEventSource.instances[0]!;
  expect(source.url).toBe('/api/v1/operations/op_1/events');
  expect(screen.getByText('Update · whoami')).toBeInTheDocument();
  expect(screen.getByText('Running')).toBeInTheDocument();

  act(() => {
    source.onopen?.();
    source.emit('output', '$ docker compose pull --ignore-buildable');
    source.emit('output', ' Image traefik/whoami Pulled');
  });
  expect(screen.getByTestId('operation-output')).toHaveTextContent(
    '$ docker compose pull --ignore-buildable Image traefik/whoami Pulled',
  );

  act(() =>
    source.emit(
      'done',
      JSON.stringify({ ...target, status: 'succeeded', exitCode: 0 }),
    ),
  );
  expect(screen.getByText('Succeeded')).toBeInTheDocument();
  expect(screen.getByText(/Exit code 0/)).toBeInTheDocument();
  expect(source.readyState).toBe(FakeEventSource.CLOSED);
  expect(
    screen.queryByRole('button', { name: 'Cancel operation' }),
  ).not.toBeInTheDocument();
});

it('replays from the start after reconnecting', () => {
  renderWithProviders(<OperationView target={target} onClose={() => {}} />);
  const source = FakeEventSource.instances[0]!;
  act(() => {
    source.onopen?.();
    source.emit('output', 'one');
    source.onerror?.();
  });
  expect(screen.getByText(/reconnecting/)).toBeInTheDocument();

  // The agent sends everything again after a reconnect.
  act(() => {
    source.onopen?.();
    source.emit('output', 'one');
    source.emit('output', 'two');
  });
  expect(screen.getByTestId('operation-output').textContent).toBe('one\ntwo');
});

it('cancels a running operation', async () => {
  const fetch = mockFetch(() => json({ ...target, status: 'running' }, 202));
  renderWithProviders(<OperationView target={target} onClose={() => {}} />);

  await userEvent.click(
    screen.getByRole('button', { name: 'Cancel operation' }),
  );
  await waitFor(() => expect(fetch).toHaveBeenCalled());
  const request = fetch.mock.calls[0]![0] as Request;
  expect(request.method).toBe('POST');
  expect(new URL(request.url).pathname).toBe('/api/v1/operations/op_1/cancel');
});

it('says who canceled an operation', () => {
  renderWithProviders(<OperationView target={target} onClose={() => {}} />);
  act(() =>
    FakeEventSource.instances[0]!.emit(
      'done',
      JSON.stringify({
        ...target,
        status: 'canceled',
        exitCode: 130,
        canceledBy: 'op_2',
      }),
    ),
  );
  expect(screen.getByText('Canceled by op_2')).toBeInTheDocument();
});
