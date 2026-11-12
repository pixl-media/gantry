import { describe, expect, it } from 'vitest';
import { actionEnabled, needsConfirmation, stackActions } from './actions';

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
