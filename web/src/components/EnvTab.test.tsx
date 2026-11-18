import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expect, it } from 'vitest';
import { json, mockFetch, renderWithProviders } from '../test/render';
import { EnvTab } from './EnvTab';

/** A fake agent with a .env file; records changes and reveals. */
function fakeAgent() {
  const values: Record<string, string> = {
    DB_PASSWORD: 's3cr3t',
    DB_USER: 'admin',
  };
  let version = 'v1';
  const reveals: string[] = [];
  const patches: unknown[] = [];
  const state = () => ({
    exists: true,
    version,
    variables: Object.keys(values).map((key) => ({ key })),
  });
  const fetch = mockFetch(async (req) => {
    const path = new URL(req.url).pathname;
    if (path === '/api/v1/stacks/app/env' && req.method === 'GET') {
      return json(state());
    }
    if (path === '/api/v1/stacks/app/env/reveal') {
      const { key } = (await req.json()) as { key: string };
      reveals.push(key);
      return json({ key, value: values[key] });
    }
    if (path === '/api/v1/stacks/app/env' && req.method === 'PATCH') {
      const body = (await req.json()) as {
        version: string;
        set?: Record<string, string>;
        delete?: string[];
      };
      patches.push(body);
      if (body.version !== version) {
        return json({ error: 'changed', code: 'version_conflict' }, 409);
      }
      Object.assign(values, body.set ?? {});
      for (const k of body.delete ?? []) delete values[k];
      version = `v${Number(version.slice(1)) + 1}`;
      return json(state());
    }
    return json({ error: 'unexpected' }, 500);
  });
  return { fetch, reveals, patches };
}

function row(key: string) {
  return screen.getByText(key).closest('tr')!;
}

it('shows names only, until a value is asked for', async () => {
  const agent = fakeAgent();
  renderWithProviders(<EnvTab stack="app" editable />);
  expect(await screen.findByText('DB_PASSWORD')).toBeInTheDocument();
  expect(screen.queryByText('s3cr3t')).not.toBeInTheDocument();
  expect(agent.reveals).toEqual([]);

  await userEvent.click(
    within(row('DB_PASSWORD')).getByRole('button', {
      name: 'Show value: DB_PASSWORD',
    }),
  );
  expect(await screen.findByText('s3cr3t')).toBeInTheDocument();
  expect(agent.reveals).toEqual(['DB_PASSWORD']);
  // Only the asked-for value is loaded.
  expect(screen.queryByText('admin')).not.toBeInTheDocument();

  await userEvent.click(
    within(row('DB_PASSWORD')).getByRole('button', {
      name: 'Hide value: DB_PASSWORD',
    }),
  );
  expect(screen.queryByText('s3cr3t')).not.toBeInTheDocument();
});

it('edits a value with the current version', async () => {
  const agent = fakeAgent();
  renderWithProviders(<EnvTab stack="app" editable />);
  await userEvent.click(
    await screen.findByRole('button', { name: 'Edit: DB_USER' }),
  );
  const field = await screen.findByDisplayValue('admin');
  await userEvent.clear(field);
  await userEvent.type(field, 'root');
  await userEvent.click(screen.getByRole('button', { name: 'Save' }));

  await waitFor(() =>
    expect(agent.patches).toEqual([
      { version: 'v1', set: { DB_USER: 'root' } },
    ]),
  );
  expect(await screen.findByText('root')).toBeInTheDocument();
  expect(
    screen.getByText(/take effect when the stack is started/),
  ).toBeInTheDocument();
});

it('deletes a variable after asking', async () => {
  const agent = fakeAgent();
  renderWithProviders(<EnvTab stack="app" editable />);
  await userEvent.click(
    await screen.findByRole('button', { name: 'Delete: DB_USER' }),
  );
  await userEvent.click(
    within(screen.getByRole('dialog')).getByRole('button', { name: 'Delete' }),
  );
  await waitFor(() =>
    expect(agent.patches).toEqual([{ version: 'v1', delete: ['DB_USER'] }]),
  );
  await waitFor(() =>
    expect(screen.queryByText('DB_USER')).not.toBeInTheDocument(),
  );
});

it('validates new variable names', async () => {
  fakeAgent();
  renderWithProviders(<EnvTab stack="app" editable />);
  await userEvent.click(
    await screen.findByRole('button', { name: 'Add variable' }),
  );
  const name = screen.getByRole('textbox', { name: 'Name' });
  await userEvent.type(name, '1BAD');
  expect(
    screen.getByText('Letters, digits and _, not starting with a digit.'),
  ).toBeInTheDocument();
  await userEvent.clear(name);
  await userEvent.type(name, 'DB_USER');
  expect(
    screen.getByText('A variable with this name exists.'),
  ).toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled();
});

it('saves with Enter and cancels with Escape', async () => {
  const agent = fakeAgent();
  renderWithProviders(<EnvTab stack="app" editable />);
  await userEvent.click(
    await screen.findByRole('button', { name: 'Add variable' }),
  );
  await userEvent.type(screen.getByRole('textbox', { name: 'Name' }), 'NEW');
  await userEvent.type(
    screen.getByRole('textbox', { name: 'Value' }),
    'value{Enter}',
  );
  await waitFor(() =>
    expect(agent.patches).toEqual([{ version: 'v1', set: { NEW: 'value' } }]),
  );
  expect(await screen.findByText('NEW')).toBeInTheDocument();

  await userEvent.click(screen.getByRole('button', { name: 'Edit: DB_USER' }));
  await userEvent.type(await screen.findByDisplayValue('admin'), 'x{Escape}');
  expect(screen.queryByDisplayValue('adminx')).not.toBeInTheDocument();
  expect(agent.patches).toHaveLength(1);
});

it('only shows values when the agent may not change the file', async () => {
  fakeAgent();
  renderWithProviders(<EnvTab stack="app" editable={false} />);
  await screen.findByText('DB_USER');
  const r = within(row('DB_USER'));
  expect(
    r.getByRole('button', { name: 'Show value: DB_USER' }),
  ).toBeInTheDocument();
  expect(r.queryByRole('button', { name: /Edit/ })).not.toBeInTheDocument();
  expect(r.queryByRole('button', { name: /Delete/ })).not.toBeInTheDocument();
  expect(screen.queryByRole('button', { name: /Add/ })).not.toBeInTheDocument();
});
