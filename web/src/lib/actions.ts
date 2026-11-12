import type { StackAction, StackSummary } from '../api/client';

/** All actions, in the order they are shown. */
export const stackActions: readonly StackAction[] = [
  'start',
  'stop',
  'restart',
  'update',
  'down',
];

/** stop and down take precedence over a running operation (agent rule). */
export function preempts(action: StackAction): boolean {
  return action === 'stop' || action === 'down';
}

/**
 * Whether an action can be started on a stack. Mirrors the agent: only
 * managed stacks can be changed, and while an operation runs only stop and
 * down are accepted.
 */
export function actionEnabled(
  stack: Pick<StackSummary, 'managed' | 'activeOperation'>,
  action: StackAction,
): boolean {
  if (!stack.managed) return false;
  if (stack.activeOperation) return preempts(action);
  return true;
}

/** Actions that ask before running. */
export function needsConfirmation(action: StackAction): boolean {
  return action === 'down';
}
