import { describe, expect, it } from 'vitest';
import type { Service } from '../api/client';
import {
  actionEnabled,
  needsConfirmation,
  serviceActionEnabled,
  serviceActions,
  stackActions,
} from './actions';

describe('actionEnabled', () => {
  it('allows every action on an idle managed stack', () => {
    for (const a of stackActions) {
      expect(actionEnabled({ managed: true }, a)).toBe(true);
    }
  });

  it('allows nothing on unmanaged stacks', () => {
    for (const a of stackActions) {
      expect(actionEnabled({ managed: false }, a)).toBe(false);
    }
  });

  it('allows only stop and down while an operation runs', () => {
    const busy = {
      managed: true,
      activeOperation: { id: 'op_1', action: 'update' as const },
    };
    const enabled = stackActions.filter((a) => actionEnabled(busy, a));
    expect(enabled).toEqual(['stop', 'down']);
  });
});

it('asks before down only', () => {
  expect(stackActions.filter(needsConfirmation)).toEqual(['down']);
});

describe('serviceActionEnabled', () => {
  const container = (state: string) => ({
    id: state,
    name: `app-${state}-1`,
    state,
    status: '',
    health: 'none' as const,
    image: 'alpine',
    ports: [],
  });
  const running: Service = { name: 'db', containers: [container('running')] };
  const stopped: Service = { name: 'db', containers: [container('exited')] };
  const none: Service = { name: 'db', containers: [] };
  const enabled = (
    stack: Parameters<typeof serviceActionEnabled>[0],
    svc: Service,
  ) => serviceActions.filter((a) => serviceActionEnabled(stack, svc, a));

  it('allows everything on a running service', () => {
    expect(enabled({ managed: true }, running)).toEqual(serviceActions);
  });

  it('needs a running container to stop or restart', () => {
    expect(enabled({ managed: true }, stopped)).toEqual(['start', 'update']);
    expect(enabled({ managed: true }, none)).toEqual(['start', 'update']);
  });

  it('allows nothing on unmanaged stacks or while an operation runs', () => {
    expect(enabled({ managed: false }, running)).toEqual([]);
    const busy = {
      managed: true,
      activeOperation: { id: 'op_1', action: 'stop' as const },
    };
    expect(enabled(busy, running)).toEqual([]);
  });
});
