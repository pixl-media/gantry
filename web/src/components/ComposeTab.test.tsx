import { act, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, expect, it, vi } from 'vitest';
import type { StackDetail } from '../api/client';
import {
  FakeEventSource,
  json,
  mockFetch,
  renderWithDataRouter,
} from '../test/render';
import { ComposeTab } from './ComposeTab';

vi.mock('./CodeEditor', () => import('../test/codeEditorStub'));

beforeEach(() => {
  vi.stubGlobal('EventSource', FakeEventSource);
});

function stack(overrides: Partial<StackDetail> = {}): StackDetail {
  return {
    name: 'app',
    managed: true,
    status: 'running',
    servicesTotal: 1,
    servicesRunning: 1,
    path: '/s/app',
    services: [],
    volumes: [],
    dockerAvailable: true,
    warnings: [],
    composeFile: {
      path: '/s/app/compose.yaml',
      content: 'services: {}\n',
      version: 'v1',
    },
    ...overrides,
  };
}

async function editor() {
  return screen.findByLabelText('Compose file');
}

it('saves the draft with the version it was based on', async () => {
  const fetch = mockFetch(() => json({ version: 'v2' }));
  renderWithDataRouter(<ComposeTab stack={stack()} />);

  const save = screen.getByRole('button', { name: 'Save' });
  expect(save).toBeDisabled();
  await userEvent.type(await editor(), 'x');
  expect(screen.getByText('Unsaved changes')).toBeInTheDocument();
  await userEvent.click(save);

  await waitFor(() => expect(fetch).toHaveBeenCalled());
  const req = fetch.requests[0]!;
  expect(req.method).toBe('PUT');
  expect(new URL(req.url).pathname).toBe('/api/v1/stacks/app/compose');
  expect(await req.json()).toEqual({
    content: 'services: {}\nx',
    version: 'v1',
  });
  expect(await screen.findByText('Saved.')).toBeInTheDocument();
});

it("shows Compose's message for an invalid file", async () => {
  mockFetch(() =>
    json(
      {
        error: 'compose.yaml: services.app.ports must be a array',
        code: 'invalid_compose',
      },
      400,
    ),
  );
  renderWithDataRouter(<ComposeTab stack={stack()} />);
  await userEvent.type(await editor(), 'x');
  await userEvent.click(screen.getByRole('button', { name: 'Save' }));
  expect(
    await screen.findByText('compose.yaml: services.app.ports must be a array'),
  ).toBeInTheDocument();
  expect(screen.getByText('Compose rejected the file:')).toBeInTheDocument();
});

it('offers to reload after a version conflict', async () => {
  mockFetch(() => json({ error: 'changed', code: 'version_conflict' }, 409));
  renderWithDataRouter(<ComposeTab stack={stack()} />);
  await userEvent.type(await editor(), 'x');
  await userEvent.click(screen.getByRole('button', { name: 'Save' }));
  await userEvent.click(await screen.findByRole('button', { name: 'Reload' }));
  // The draft is gone; the stored file is shown again.
  expect(await editor()).toHaveValue('services: {}\n');
  expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled();
});

it('is read-only for unmanaged stacks', async () => {
  renderWithDataRouter(<ComposeTab stack={stack({ managed: false })} />);
  expect(await editor()).toHaveAttribute('readonly');
  expect(
    screen.queryByRole('button', { name: 'Save' }),
  ).not.toBeInTheDocument();
  expect(screen.getByText(/read-only/)).toBeInTheDocument();
});

it('asks before leaving with unsaved changes', async () => {
  const { router } = renderWithDataRouter(<ComposeTab stack={stack()} />);
  await userEvent.type(await editor(), 'x');

  await act(() => router.navigate('/'));
  expect(
    await screen.findByText('Discard unsaved changes?'),
  ).toBeInTheDocument();
  await userEvent.click(screen.getByRole('button', { name: 'Stay' }));
  expect(router.state.location.pathname).toBe('/stacks/app');

  await act(() => router.navigate('/'));
  await userEvent.click(
    await screen.findByRole('button', { name: 'Discard and leave' }),
  );
  expect(await screen.findByText('Home')).toBeInTheDocument();
});
