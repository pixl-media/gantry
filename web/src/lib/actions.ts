import type {
  Service,
  ServiceAction,
  StackAction,
  StackSummary,
} from '../api/client';

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

/** The actions on a single service, in the order they are shown. */
export const serviceActions: readonly ServiceAction[] = [
  'start',
  'stop',
  'restart',
  'update',
];

/**
 * Whether an action can be started on one service. Mirrors the agent: only
 * managed stacks, and service actions never take precedence, so nothing runs
 * while another operation does. Stopping or restarting needs a running
 * container.
 */
export function serviceActionEnabled(
  stack: Pick<StackSummary, 'managed' | 'activeOperation'>,
  service: Service,
  action: ServiceAction,
): boolean {
  if (!stack.managed || stack.activeOperation) return false;
  if (action === 'stop' || action === 'restart') {
    return service.containers.some((c) => c.state === 'running');
  }
  return true;
}
