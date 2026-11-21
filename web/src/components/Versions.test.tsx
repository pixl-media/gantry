import { screen } from '@testing-library/react';
import { expect, it } from 'vitest';
import { json, mockFetch, renderWithProviders } from '../test/render';
import { Versions } from './Versions';

it("shows the web UI's and the agent's version", async () => {
  mockFetch((req) =>
    new URL(req.url).pathname === '/api/v1/version'
      ? json({ version: '0.2.0' })
      : json({ error: 'unexpected' }, 500),
  );
  renderWithProviders(<Versions />);
  // Tests run without a release build: the web UI is "dev", which is never
  // called a different release.
  expect(
    await screen.findByText('Web UI dev · agent 0.2.0'),
  ).toBeInTheDocument();
});
