import { screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, expect, it } from 'vitest';
import { Route, Routes, useLocation } from 'react-router';
import { json, mockFetch, renderWithProviders } from '../test/render';
import { StacksPage } from './StacksPage';

const stack = (name: string, managed: boolean) => ({
  name,
  managed,
  status: 'running',
  servicesRunning: 1,
  servicesTotal: 2,
  path: `/opt/stacks/${name}`,
});

function fakeAgent() {
  return mockFetch((req) => {
    if (new URL(req.url).pathname === '/api/v1/stacks') {
      return json({
        stacks: [
          stack('web-shop', true),
          stack('db', true),
          stack('webmail', false),
        ],
        dockerAvailable: true,
        warnings: [],
      });
    }
    return json({ error: 'unexpected' }, 500);
  });
}

/** Shows the current URL, so tests can check what the page put there. */
function Where() {
  const l = useLocation();
  return <div data-testid="where">{l.pathname + l.search}</div>;
}

function renderList(route = '/') {
  fakeAgent();
  renderWithProviders(
    <>
      <Routes>
        <Route path="/" element={<StacksPage />} />
        <Route path="/stacks/:name" element={<div>Stack page</div>} />
      </Routes>
      <Where />
    </>,
    route,
  );
}

const where = () => screen.getByTestId('where').textContent;

beforeEach(() => localStorage.clear());

it('filters by type and keeps it in the URL', async () => {
  renderList();
  await screen.findByText('webmail');
  await userEvent.click(screen.getByRole('button', { name: 'External' }));
  expect(screen.getByText('webmail')).toBeInTheDocument();
  expect(screen.queryByText('web-shop')).not.toBeInTheDocument();
  expect(screen.getByText('1 of 3')).toBeInTheDocument();
  expect(where()).toBe('/?type=external');
});

it('searches by name', async () => {
  renderList();
  await userEvent.type(
    await screen.findByRole('searchbox', { name: 'Search' }),
    'WEB',
  );
  expect(screen.getByText('web-shop')).toBeInTheDocument();
  expect(screen.getByText('webmail')).toBeInTheDocument();
  expect(screen.queryByText('db')).not.toBeInTheDocument();
  expect(where()).toBe('/?q=WEB');
});

it('starts with the filter from the URL, and resets it', async () => {
  renderList('/?type=external&q=shop');
  expect(
    await screen.findByText('No stack matches the filter.'),
  ).toBeInTheDocument();
  expect(screen.getByRole('searchbox', { name: 'Search' })).toHaveValue('shop');
  await userEvent.click(screen.getByRole('button', { name: 'Reset filter' }));
  expect(screen.getByText('web-shop')).toBeInTheDocument();
  expect(screen.getByText('db')).toBeInTheDocument();
  expect(where()).toBe('/');
});

it('shows cards and remembers the choice', async () => {
  renderList();
  await screen.findByText('web-shop');
  expect(screen.getByRole('table')).toBeInTheDocument();
  await userEvent.click(screen.getByRole('button', { name: 'Cards' }));
  expect(screen.queryByRole('table')).not.toBeInTheDocument();
  expect(screen.getAllByText('1/2 services')).toHaveLength(3);
  expect(localStorage.getItem('gantry.stacksView')).toBe('cards');

  // A managed stack's card has its actions; an external one's has none.
  const shop = screen
    .getByRole('link', { name: 'web-shop' })
    .closest('.MuiCard-root');
  const mail = screen
    .getByRole('link', { name: 'webmail' })
    .closest('.MuiCard-root');
  expect(
    within(shop as HTMLElement).getByRole('button', { name: 'Start' }),
  ).toBeInTheDocument();
  expect(
    within(mail as HTMLElement).queryByRole('button', { name: 'Start' }),
  ).not.toBeInTheDocument();

  // The card's link opens the stack.
  await userEvent.click(screen.getByRole('link', { name: 'db' }));
  expect(await screen.findByText('Stack page')).toBeInTheDocument();
  expect(where()).toBe('/stacks/db');
});

it('starts in the remembered view', async () => {
  localStorage.setItem('gantry.stacksView', 'cards');
  renderList();
  await screen.findByText('web-shop');
  expect(screen.queryByRole('table')).not.toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Cards' })).toHaveAttribute(
    'aria-pressed',
    'true',
  );
});
