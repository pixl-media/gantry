import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, expect, it, vi } from 'vitest';
import {
  FakeEventSource,
  json,
  mockFetch,
  renderWithProviders,
} from '../test/render';
import { composeTemplate, CreateStackDialog } from './CreateStackDialog';
import { DeleteStackDialog } from './DeleteStackDialog';

vi.mock('./CodeEditor', () => import('../test/codeEditorStub'));

beforeEach(() => {
  vi.stubGlobal('EventSource', FakeEventSource);
});

it('validates the name of a new stack', async () => {
  renderWithProviders(
    <CreateStackDialog open onClose={() => {}} existing={['taken']} />,
  );
  const name = screen.getByRole('textbox', { name: /Name/ });
  const create = screen.getByRole('button', { name: 'Create' });
  expect(create).toBeDisabled();

  await userEvent.type(name, 'Bad Name');
  expect(screen.getByRole('textbox', { name: /Name/ })).toHaveAttribute(
    'aria-invalid',
    'true',
  );
  await userEvent.clear(name);
  await userEvent.type(name, 'taken');
  expect(
    screen.getByText('A stack with this name exists.'),
  ).toBeInTheDocument();
  await userEvent.clear(name);
  await userEvent.type(name, 'fresh-stack');
  expect(create).toBeEnabled();
});

it('creates a stack with variables', async () => {
  const fetch = mockFetch(() => json({ name: 'fresh', path: '/s/fresh' }, 201));
  const onClose = vi.fn();
  renderWithProviders(
    <CreateStackDialog open onClose={onClose} existing={[]} />,
  );
  await userEvent.type(screen.getByRole('textbox', { name: /Name/ }), 'fresh');
  await userEvent.click(screen.getByRole('button', { name: 'Add variable' }));
  await userEvent.type(screen.getByPlaceholderText('NAME'), 'TOKEN');
  await userEvent.type(screen.getByRole('textbox', { name: 'Value' }), 'abc');
  await userEvent.click(screen.getByLabelText('Start after creating')); // off
  await userEvent.click(screen.getByRole('button', { name: 'Create' }));

  await waitFor(() => expect(onClose).toHaveBeenCalled());
  const req = fetch.requests[0]!;
  expect(new URL(req.url).pathname).toBe('/api/v1/stacks');
  expect(await req.json()).toEqual({
    name: 'fresh',
    compose: composeTemplate,
    env: { TOKEN: 'abc' },
  });
  expect(fetch).toHaveBeenCalledTimes(1); // not started
});

it('starts a new stack before leaving the dialog', async () => {
  const fetch = mockFetch((req) =>
    new URL(req.url).pathname === '/api/v1/stacks'
      ? json({ name: 'fresh', path: '/s/fresh' }, 201)
      : json(
          {
            id: 'op_1',
            stack: 'fresh',
            action: 'start',
            status: 'running',
            startedAt: '2026-11-13T12:00:00Z',
          },
          202,
        ),
  );
  // The page that opened the dialog may unmount on close, so the start
  // must have been requested by then.
  let requestsAtClose = 0;
  const onClose = vi.fn(() => {
    requestsAtClose = fetch.requests.length;
  });
  renderWithProviders(
    <CreateStackDialog open onClose={onClose} existing={[]} />,
  );
  await userEvent.type(screen.getByRole('textbox', { name: /Name/ }), 'fresh');
  await userEvent.click(screen.getByRole('button', { name: 'Create' }));

  await waitFor(() => expect(onClose).toHaveBeenCalled());
  expect(requestsAtClose).toBe(2);
  expect(fetch.requests.map((r) => new URL(r.url).pathname)).toEqual([
    '/api/v1/stacks',
    '/api/v1/stacks/fresh/actions/start',
  ]);
  // The operation panel shows the start's output.
  expect(await screen.findByText('Start · fresh')).toBeInTheDocument();
});

it('shows why Compose rejected a new stack', async () => {
  mockFetch(() =>
    json({ error: 'compose.yaml: yaml: line 2', code: 'invalid_compose' }, 400),
  );
  renderWithProviders(
    <CreateStackDialog open onClose={() => {}} existing={[]} />,
  );
  await userEvent.type(screen.getByRole('textbox', { name: /Name/ }), 'x');
  await userEvent.click(screen.getByRole('button', { name: 'Create' }));
  expect(
    await screen.findByText('compose.yaml: yaml: line 2'),
  ).toBeInTheDocument();
});

const target = { name: 'app', path: '/s/app', volumes: ['app_data'] };

it('deletes only after the name was typed', async () => {
  const fetch = mockFetch(() =>
    json(
      {
        id: 'op_1',
        stack: 'app',
        action: 'delete',
        status: 'running',
        startedAt: '2026-11-13T12:00:00Z',
      },
      202,
    ),
  );
  renderWithProviders(
    <DeleteStackDialog open onClose={() => {}} stack={target} />,
  );
  const submit = screen.getByRole('button', { name: 'Delete permanently' });
  expect(submit).toBeDisabled();
  expect(screen.getByText('app_data')).toBeInTheDocument();

  await userEvent.type(screen.getByLabelText('Type app to confirm'), 'ap');
  expect(submit).toBeDisabled();
  await userEvent.type(screen.getByLabelText('Type app to confirm'), 'p');
  await userEvent.click(submit);

  await waitFor(() => expect(fetch).toHaveBeenCalled());
  const url = new URL((fetch.mock.calls[0]![0] as Request).url);
  expect(url.pathname).toBe('/api/v1/stacks/app');
  expect(url.searchParams.get('confirm')).toBe('app');
  expect(url.searchParams.get('volumes')).toBe('false');
});

it('deletes volumes only when checked', async () => {
  const fetch = mockFetch(() =>
    json(
      {
        id: 'op_1',
        stack: 'app',
        action: 'delete',
        status: 'running',
        startedAt: '2026-11-13T12:00:00Z',
      },
      202,
    ),
  );
  renderWithProviders(
    <DeleteStackDialog open onClose={() => {}} stack={target} />,
  );
  expect(
    screen.getByText('These named volumes are kept unless you check this:'),
  ).toBeInTheDocument();
  await userEvent.click(screen.getByLabelText('Also delete volumes'));
  expect(
    screen.getByText('These named volumes will be deleted too:'),
  ).toBeInTheDocument();
  await userEvent.type(screen.getByLabelText('Type app to confirm'), 'app');
  await userEvent.click(
    screen.getByRole('button', { name: 'Delete permanently' }),
  );
  await waitFor(() => expect(fetch).toHaveBeenCalled());
  const url = new URL((fetch.mock.calls[0]![0] as Request).url);
  expect(url.searchParams.get('volumes')).toBe('true');
});
