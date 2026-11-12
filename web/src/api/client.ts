import createFetchClient, { type Middleware } from 'openapi-fetch';
import createClient from 'openapi-react-query';
import type { components, paths } from './schema.gen';

export type StackSummary = components['schemas']['StackSummary'];
export type StackDetail = components['schemas']['StackDetail'];
export type StackAction = components['schemas']['StackAction'];
export type StackStatus = components['schemas']['StackStatus'];
export type Operation = components['schemas']['Operation'];
export type OperationStatus = components['schemas']['OperationStatus'];
export type ApiError = components['schemas']['Error'];
export type Container = components['schemas']['Container'];

let onUnauthorized: () => void = () => {};

/** Called when the agent answers 401, e.g. because the session expired. */
export function setUnauthorizedHandler(handler: () => void) {
  onUnauthorized = handler;
}

const unauthorized: Middleware = {
  onResponse({ request, response }) {
    // The session endpoint answers 401 by design when logged out.
    if (response.status === 401 && !request.url.endsWith('/api/v1/session')) {
      onUnauthorized();
    }
  },
};

// Same origin as the page: the session cookie is sent automatically, and the
// token is never handled by this code after logging in.
export const fetchClient = createFetchClient<paths>({
  baseUrl: window.location.origin,
  credentials: 'same-origin',
  // Resolved per call, so tests can replace fetch.
  fetch: (request) => globalThis.fetch(request),
});
fetchClient.use(unauthorized);

export const $api = createClient(fetchClient);

/** Reads an API error from whatever a failed request threw. */
export function apiError(error: unknown): ApiError | undefined {
  if (error && typeof error === 'object' && 'error' in error) {
    return error as ApiError;
  }
  return undefined;
}
