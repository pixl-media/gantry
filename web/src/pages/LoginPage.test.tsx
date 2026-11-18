import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expect, it } from 'vitest';
import { Route, Routes } from 'react-router';
import { json, mockFetch, renderWithProviders } from '../test/render';
import { LoginPage } from './LoginPage';

/**
 * A fake agent: logged out until the right token is posted. methods are the
 * ways in it offers.
 */
function fakeAgent({
  webLogin = true,
  methods = { token: true, oidc: false },
} = {}) {
  let loggedIn = false;
  return mockFetch(async (req) => {
    const path = new URL(req.url).pathname;
    if (path === '/api/v1/session/methods') return json(methods);
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

function renderLogin(route = '/login') {
  renderWithProviders(
    <Routes>
      <Route path="/login" element={<LoginPage />} />
      <Route path="/" element={<div>Stacks home</div>} />
    </Routes>,
    route,
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

it('says when the agent refuses the token login', async () => {
  fakeAgent({ webLogin: false });
  renderLogin();
  await userEvent.type(await screen.findByLabelText(/Token/), 'right');
  await userEvent.click(screen.getByRole('button', { name: 'Sign in' }));
  expect(
    await screen.findByText(/Signing in with a token is off/),
  ).toBeInTheDocument();
});

it('says when there is no way in', async () => {
  fakeAgent({ methods: { token: false, oidc: false } });
  renderLogin();
  expect(await screen.findByText(/The web login is off/)).toBeInTheDocument();
  expect(screen.queryByLabelText(/Token/)).not.toBeInTheDocument();
});

it('offers single sign-on alone', async () => {
  fakeAgent({ methods: { token: false, oidc: true } });
  renderLogin();
  const sso = await screen.findByRole('link', {
    name: 'Sign in with single sign-on',
  });
  // A plain navigation to the agent, which redirects to the provider.
  expect(sso).toHaveAttribute('href', '/api/v1/session/oidc?returnTo=%2F');
  expect(screen.queryByLabelText(/Token/)).not.toBeInTheDocument();
});

it('offers single sign-on and the token', async () => {
  fakeAgent({ methods: { token: true, oidc: true } });
  renderLogin();
  expect(
    await screen.findByRole('link', { name: 'Sign in with single sign-on' }),
  ).toBeInTheDocument();
  expect(screen.getByText('or')).toBeInTheDocument();
  expect(screen.getByLabelText(/Token/)).toBeInTheDocument();
});

it('says why single sign-on failed', async () => {
  fakeAgent({ methods: { token: false, oidc: true } });
  renderLogin('/login?error=oidc_denied');
  expect(
    await screen.findByText(/Your account isn't allowed to use gantry/),
  ).toBeInTheDocument();
});

it('has a message for unknown errors from single sign-on', async () => {
  fakeAgent({ methods: { token: false, oidc: true } });
  renderLogin('/login?error=something_new');
  expect(await screen.findByText(/Single sign-on failed/)).toBeInTheDocument();
});
