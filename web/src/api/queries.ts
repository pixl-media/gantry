import { useQueryClient } from '@tanstack/react-query';
import { $api } from './client';
import { useRefreshInterval } from './live';

export function useSession() {
  return $api.useQuery('get', '/api/v1/session', {}, { retry: false });
}

/** Which ways to log in the agent offers (public). */
export function useLoginMethods() {
  return $api.useQuery(
    'get',
    '/api/v1/session/methods',
    {},
    { retry: false, staleTime: Infinity },
  );
}

// gcTime 0 on mutations whose variables hold the token or .env values:
// TanStack keeps a finished mutation, variables included, for 5 minutes by
// default; this drops it once the component is gone.

export function useLogin() {
  const queryClient = useQueryClient();
  return $api.useMutation('post', '/api/v1/session', {
    gcTime: 0,
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
  const refetchInterval = useRefreshInterval();
  return $api.useQuery('get', '/api/v1/stacks', {}, { refetchInterval });
}

export function useStack(name: string) {
  const refetchInterval = useRefreshInterval();
  return $api.useQuery(
    'get',
    '/api/v1/stacks/{name}',
    { params: { path: { name } } },
    { refetchInterval, retry: false },
  );
}

export function useStackOperations(stack: string) {
  const refetchInterval = useRefreshInterval();
  return $api.useQuery(
    'get',
    '/api/v1/operations',
    { params: { query: { stack } } },
    { refetchInterval },
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

export function useRunServiceAction() {
  const invalidate = useInvalidateState();
  return $api.useMutation(
    'post',
    '/api/v1/stacks/{name}/services/{service}/actions/{action}',
    { onSettled: invalidate },
  );
}

export function useUpdateCompose() {
  const invalidate = useInvalidateState();
  return $api.useMutation('put', '/api/v1/stacks/{name}/compose', {
    onSettled: invalidate,
  });
}

/** Variable names of a stack's .env file (never values). */
export function useEnv(name: string, enabled = true) {
  return $api.useQuery(
    'get',
    '/api/v1/stacks/{name}/env',
    { params: { path: { name } } },
    { enabled },
  );
}

export function useUpdateEnv(name: string) {
  const queryClient = useQueryClient();
  return $api.useMutation('patch', '/api/v1/stacks/{name}/env', {
    gcTime: 0,
    // The answer is the new state: keep the version current for the next
    // change.
    onSuccess: (data) =>
      queryClient.setQueryData(
        ['get', '/api/v1/stacks/{name}/env', { params: { path: { name } } }],
        data,
      ),
  });
}

/** Reads one value on purpose. Never cached: the result stays in the caller. */
export function useRevealEnv() {
  return $api.useMutation('post', '/api/v1/stacks/{name}/env/reveal', {
    gcTime: 0,
  });
}

export function useCreateStack() {
  const invalidate = useInvalidateState();
  return $api.useMutation('post', '/api/v1/stacks', {
    gcTime: 0,
    onSettled: invalidate,
  });
}

export function useDeleteStack() {
  const invalidate = useInvalidateState();
  return $api.useMutation('delete', '/api/v1/stacks/{name}', {
    onSettled: invalidate,
  });
}

export function useCancelOperation() {
  const invalidate = useInvalidateState();
  return $api.useMutation('post', '/api/v1/operations/{id}/cancel', {
    onSettled: invalidate,
  });
}
