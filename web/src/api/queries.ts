import { useQueryClient } from '@tanstack/react-query';
import { $api } from './client';

/** How often lists and details refresh while visible. */
export const refreshInterval = 5000;

export function useSession() {
  return $api.useQuery('get', '/api/v1/session', {}, { retry: false });
}

export function useLogin() {
  const queryClient = useQueryClient();
  return $api.useMutation('post', '/api/v1/session', {
    onSuccess: () => queryClient.invalidateQueries(),
  });
}

export function useLogout() {
  const queryClient = useQueryClient();
  return $api.useMutation('delete', '/api/v1/session', {
    onSettled: () => queryClient.clear(),
  });
}

export function useStacks() {
  return $api.useQuery(
    'get',
    '/api/v1/stacks',
    {},
    { refetchInterval: refreshInterval },
  );
}

export function useStack(name: string) {
  return $api.useQuery(
    'get',
    '/api/v1/stacks/{name}',
    { params: { path: { name } } },
    { refetchInterval: refreshInterval, retry: false },
  );
}

export function useStackOperations(stack: string) {
  return $api.useQuery(
    'get',
    '/api/v1/operations',
    { params: { query: { stack } } },
    { refetchInterval: refreshInterval },
  );
}

export function useOperation(id: string) {
  return $api.useQuery('get', '/api/v1/operations/{id}', {
    params: { path: { id } },
  });
}

/** Refreshes everything that shows stack or operation state. */
function useInvalidateState() {
  const queryClient = useQueryClient();
  return () =>
    Promise.all([
      queryClient.invalidateQueries({ queryKey: ['get', '/api/v1/stacks'] }),
      queryClient.invalidateQueries({
        queryKey: ['get', '/api/v1/stacks/{name}'],
      }),
      queryClient.invalidateQueries({
        queryKey: ['get', '/api/v1/operations'],
      }),
    ]);
}

export function useRunAction() {
  const invalidate = useInvalidateState();
  return $api.useMutation('post', '/api/v1/stacks/{name}/actions/{action}', {
    onSettled: invalidate,
  });
}

export function useCancelOperation() {
  const invalidate = useInvalidateState();
  return $api.useMutation('post', '/api/v1/operations/{id}/cancel', {
    onSettled: invalidate,
  });
}
