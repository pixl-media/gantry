import { expect, it } from 'vitest';
import { chownCommand } from './fileAccess';

it('changes the owner of exactly the missing paths, never recursively', () => {
  expect(
    chownCommand({
      user: 'gantry',
      missing: [
        { path: '/opt/stacks/romm', access: 'write' },
        { path: '/opt/stacks/romm/.env', access: 'read' },
      ],
    }),
  ).toBe('sudo chown gantry /opt/stacks/romm /opt/stacks/romm/.env');
});

it('quotes paths for the shell', () => {
  expect(
    chownCommand({
      user: '998',
      missing: [{ path: "/srv/my stacks/it's", access: 'write' }],
    }),
  ).toBe(`sudo chown 998 '/srv/my stacks/it'\\''s'`);
});

it('leaves out paths on read-only file systems', () => {
  const ro = {
    path: '/etc/stacks',
    access: 'write',
    readOnlyFilesystem: true,
  } as const;
  expect(chownCommand({ user: 'gantry', missing: [ro] })).toBeUndefined();
  expect(
    chownCommand({
      user: 'gantry',
      missing: [ro, { path: '/etc/stacks/app/.env', access: 'read' }],
    }),
  ).toBe('sudo chown gantry /etc/stacks/app/.env');
  expect(chownCommand({ user: 'gantry', missing: [] })).toBeUndefined();
});
