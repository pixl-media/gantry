import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, expect, it, vi } from 'vitest';
import type { Service } from '../api/client';
import {
  FakeEventSource,
  json,
  mockFetch,
  renderWithProviders,
} from '../test/render';
import { ServiceMenu } from './ServiceMenu';

beforeEach(() => {
  vi.stubGlobal('EventSource', FakeEventSource);
});

const db: Service = {
  name: 'db',
  containers: [
    {
      id: 'c1',
      name: 'app-db-1',
      state: 'running',
      status: 'Up 2 hours',
      health: 'none',
      image: 'postgres:17',
      ports: [],
    },
  ],
};

const operation = {
  id: 'op_9',
  stack: 'app',
  action: 'restart',
  status: 'running',
  startedAt: '2026-11-16T12:00:00Z',
  service: 'db',
  services: ['db', 'web'],
};

const plan = {
  service: 'db',
  action: 'restart',
  affected: [
    { service: 'db', effect: 'restart', reason: 'target' },
    { service: 'web', effect: 'restart', reason: 'dependent' },
  ],
};

it('shows nothing for unmanaged stacks', () => {
  const { container } = renderWithProviders(
    <ServiceMenu stack={{ name: 'ext', managed: false }} service={db} />,
  );
  expect(container).toBeEmptyDOMElement();
});

it('is disabled while an operation runs', () => {
  renderWithProviders(
    <ServiceMenu
      stack={{
        name: 'app',
        managed: true,
        activeOperation: { id: 'op_1', action: 'update' },
      }}
      service={db}
    />,
  );
  expect(screen.getByRole('button', { name: 'Actions for db' })).toBeDisabled();
});

it('offers only start and update for a stopped service', async () => {
  renderWithProviders(
    <ServiceMenu
      stack={{ name: 'app', managed: true }}
      service={{ name: 'db', containers: [] }}
    />,
  );
  await userEvent.click(screen.getByRole('button', { name: 'Actions for db' }));
  const menu = screen.getByRole('menu');
  const disabled = within(menu)
    .getAllByRole('menuitem')
    .filter((i) => i.getAttribute('aria-disabled') === 'true')
    .map((i) => i.textContent);
  expect(disabled).toEqual([
    expect.stringContaining('Stop'),
    expect.stringContaining('Restart'),
  ]);
});

it('runs an action that affects only this service right away', async () => {
  const fetch = mockFetch(() => json({ ...operation, services: ['db'] }, 202));
  renderWithProviders(
    <ServiceMenu stack={{ name: 'app', managed: true }} service={db} />,
  );
  await userEvent.click(screen.getByRole('button', { name: 'Actions for db' }));
  await userEvent.click(screen.getByRole('menuitem', { name: /Restart/ }));

  await waitFor(() => expect(fetch).toHaveBeenCalledTimes(1));
  expect(new URL(fetch.requests[0]!.url).pathname).toBe(
    '/api/v1/stacks/app/services/db/actions/restart',
  );
  expect(await screen.findByText('Restart · app / db')).toBeInTheDocument();
});

it('shows the affected services and confirms them', async () => {
  const fetch = mockFetch(async (request) => {
    const body = await request.text();
    return body.includes('confirm')
      ? json(operation, 202)
      : json({ error: 'confirm', code: 'confirmation_required', plan }, 409);
  });
  renderWithProviders(
    <ServiceMenu stack={{ name: 'app', managed: true }} service={db} />,
  );
  await userEvent.click(screen.getByRole('button', { name: 'Actions for db' }));
  await userEvent.click(screen.getByRole('menuitem', { name: /Restart/ }));

  const dialog = await screen.findByRole('dialog');
  expect(within(dialog).getByText('Restart db?')).toBeInTheDocument();
  expect(within(dialog).getByText('web – restarted')).toBeInTheDocument();
  expect(within(dialog).getByText('needs db')).toBeInTheDocument();

  await userEvent.click(
    within(dialog).getByRole('button', { name: 'Restart 2 services' }),
  );
  await waitFor(() => expect(fetch).toHaveBeenCalledTimes(2));
  expect(await fetch.requests[1]!.json()).toEqual({ confirm: ['db', 'web'] });
  expect(await screen.findByText('Restart · app / db')).toBeInTheDocument();
  await waitFor(() =>
    expect(screen.queryByText('Restart db?')).not.toBeInTheDocument(),
  );
});

it('shows a new plan if the state changed meanwhile', async () => {
  mockFetch(() =>
    json({ error: 'confirm', code: 'confirmation_required', plan }, 409),
  );
  renderWithProviders(
    <ServiceMenu stack={{ name: 'app', managed: true }} service={db} />,
  );
  await userEvent.click(screen.getByRole('button', { name: 'Actions for db' }));
  await userEvent.click(screen.getByRole('menuitem', { name: /Restart/ }));
  await userEvent.click(
    await screen.findByRole('button', { name: 'Restart 2 services' }),
  );
  expect(
    await screen.findByText(/state changed in the meantime/),
  ).toBeInTheDocument();
});
