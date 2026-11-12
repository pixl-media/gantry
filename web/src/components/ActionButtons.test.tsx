import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, expect, it, vi } from 'vitest';
import {
  FakeEventSource,
  json,
  mockFetch,
  renderWithProviders,
} from '../test/render';
import { ActionButtons } from './ActionButtons';

beforeEach(() => {
  vi.stubGlobal('EventSource', FakeEventSource);
});

it('shows nothing for unmanaged stacks', () => {
  const { container } = renderWithProviders(
    <ActionButtons stack={{ name: 'ext', managed: false }} />,
  );
  expect(container).toBeEmptyDOMElement();
});

it('allows only stop and down while an operation runs', () => {
  renderWithProviders(
    <ActionButtons
      stack={{
        name: 'app',
        managed: true,
        activeOperation: { id: 'op_1', action: 'update' },
      }}
    />,
  );
  for (const name of ['Start', 'Restart', 'Update']) {
    expect(screen.getByRole('button', { name })).toBeDisabled();
  }
  for (const name of ['Stop', 'Down']) {
    expect(screen.getByRole('button', { name })).toBeEnabled();
  }
});

it('starts an action and opens the output panel', async () => {
  const fetch = mockFetch(() =>
    json(
      {
        id: 'op_9',
        stack: 'app',
        action: 'start',
        status: 'running',
        startedAt: '2026-11-12T12:00:00Z',
      },
      202,
    ),
  );
  renderWithProviders(<ActionButtons stack={{ name: 'app', managed: true }} />);

  await userEvent.click(screen.getByRole('button', { name: 'Start' }));
  await waitFor(() => expect(fetch).toHaveBeenCalled());
  const request = fetch.mock.calls[0]![0] as Request;
  expect(new URL(request.url).pathname).toBe(
    '/api/v1/stacks/app/actions/start',
  );
  expect(await screen.findByText('Start · app')).toBeInTheDocument();
});

it('asks before down', async () => {
  const fetch = mockFetch(() =>
    json(
      {
        id: 'op_9',
        stack: 'app',
        action: 'down',
        status: 'running',
        startedAt: '2026-11-12T12:00:00Z',
      },
      202,
    ),
  );
  renderWithProviders(<ActionButtons stack={{ name: 'app', managed: true }} />);

  await userEvent.click(screen.getByRole('button', { name: 'Down' }));
  expect(screen.getByText('Remove the containers of app?')).toBeInTheDocument();
  expect(fetch).not.toHaveBeenCalled();

  await userEvent.click(screen.getByRole('button', { name: 'Remove' }));
  await waitFor(() => expect(fetch).toHaveBeenCalledTimes(1));
});

it('explains a rejected action', async () => {
  mockFetch(() =>
    json({ error: 'busy', code: 'stack_busy', operation: 'op_1' }, 409),
  );
  renderWithProviders(<ActionButtons stack={{ name: 'app', managed: true }} />);
  await userEvent.click(screen.getByRole('button', { name: 'Restart' }));
  expect(
    await screen.findByText('Another operation is running on this stack.'),
  ).toBeInTheDocument();
});
