import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expect, it } from 'vitest';
import { Route, Routes } from 'react-router';
import { json, mockFetch, renderWithProviders } from '../test/render';
import { LoginPage } from './LoginPage';

/** A fake agent: logged out until the right token is posted. */
function fakeAgent({ webLogin = true } = {}) {
  let loggedIn = false;
  return mockFetch(async (req) => {
    const path = new URL(req.url).pathname;
    if (path === '/api/v1/session' && req.method === 'GET') {
      return loggedIn
        ? json({ method: 'cookie', expiresAt: '2026-11-13T00:00:00Z' })
        : json({ error: 'unauthorized', code: 'unauthorized' }, 401);
    }
    if (path === '/api/v1/session' && req.method === 'POST') {
      const { token } = (await req.json()) as { token: string };
      if (!webLogin) {
        return json({ error: 'web login off', code: 'login_disabled' }, 403);
      }
      if (token !== 'right') {
        return json({ error: 'invalid token', code: 'invalid_token' }, 401);
      }
      loggedIn = true;
      return new Response(null, { status: 204 });
    }
    return json({ error: 'unexpected' }, 500);
  });
}

function renderLogin() {
  renderWithProviders(
    <Routes>
      <Route path="/login" element={<LoginPage />} />
      <Route path="/" element={<div>Stacks home</div>} />
    </Routes>,
    '/login',
  );
}

it('rejects a wrong token', async () => {
  fakeAgent();
  renderLogin();
  await userEvent.type(await screen.findByLabelText(/Token/), 'wrong');
  await userEvent.click(screen.getByRole('button', { name: 'Sign in' }));
  expect(await screen.findByText('Wrong token.')).toBeInTheDocument();
});

it('logs in and goes to the stacks', async () => {
  const fetch = fakeAgent();
  renderLogin();
  await userEvent.type(await screen.findByLabelText(/Token/), 'right');
  await userEvent.click(screen.getByRole('button', { name: 'Sign in' }));
  expect(await screen.findByText('Stacks home')).toBeInTheDocument();

  // The token is only sent to the login endpoint, as JSON body.
  const posts = fetch.mock.calls
    .map((c) => c[0] as Request)
    .filter((r) => r.method === 'POST');
  expect(posts).toHaveLength(1);
  expect(new URL(posts[0]!.url).pathname).toBe('/api/v1/session');
});

it('says when the web login is off', async () => {
  fakeAgent({ webLogin: false });
  renderLogin();
  await userEvent.type(await screen.findByLabelText(/Token/), 'right');
  await userEvent.click(screen.getByRole('button', { name: 'Sign in' }));
  expect(
    await screen.findByText(
      'The web login is off: GANTRY_UI_TOKEN is not set on the agent.',
    ),
  ).toBeInTheDocument();
});
