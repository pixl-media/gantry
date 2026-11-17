import type { StackSummary } from '../api/client';

export type StackTypeFilter = 'all' | 'managed' | 'external';

/** What the stack list shows; kept in the URL as `?type=…&q=…`. */
export interface StackFilter {
  type: StackTypeFilter;
  q: string;
}

/** Reads the filter from the URL; unknown values mean "all". */
export function parseStackFilter(params: URLSearchParams): StackFilter {
  const type = params.get('type');
  return {
    type: type === 'managed' || type === 'external' ? type : 'all',
    q: params.get('q') ?? '',
  };
}

/** The URL parameters for a filter, without defaults. */
export function stackFilterParams(f: StackFilter): URLSearchParams {
  const params = new URLSearchParams();
  if (f.type !== 'all') params.set('type', f.type);
  if (f.q !== '') params.set('q', f.q);
  return params;
}

export function isFiltered(f: StackFilter): boolean {
  return f.type !== 'all' || f.q.trim() !== '';
}

/** The stacks of the type whose name contains q, ignoring case. */
export function filterStacks<T extends Pick<StackSummary, 'name' | 'managed'>>(
  list: T[],
  f: StackFilter,
): T[] {
  const q = f.q.trim().toLowerCase();
  return list.filter(
    (s) =>
      (f.type === 'all' || (f.type === 'managed') === s.managed) &&
      s.name.toLowerCase().includes(q),
  );
}
