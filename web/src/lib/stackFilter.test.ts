import { describe, expect, it } from 'vitest';
import {
  filterStacks,
  isFiltered,
  parseStackFilter,
  stackFilterParams,
} from './stackFilter';

const list = [
  { name: 'web-shop', managed: true },
  { name: 'Webmail', managed: false },
  { name: 'db', managed: true },
];
const names = (l: { name: string }[]) => l.map((s) => s.name);

describe('filterStacks', () => {
  it('filters by type', () => {
    expect(names(filterStacks(list, { type: 'all', q: '' }))).toEqual([
      'web-shop',
      'Webmail',
      'db',
    ]);
    expect(names(filterStacks(list, { type: 'managed', q: '' }))).toEqual([
      'web-shop',
      'db',
    ]);
    expect(names(filterStacks(list, { type: 'external', q: '' }))).toEqual([
      'Webmail',
    ]);
  });

  it('searches names, ignoring case and outer spaces', () => {
    expect(names(filterStacks(list, { type: 'all', q: ' WEB ' }))).toEqual([
      'web-shop',
      'Webmail',
    ]);
    expect(names(filterStacks(list, { type: 'managed', q: 'web' }))).toEqual([
      'web-shop',
    ]);
    expect(filterStacks(list, { type: 'all', q: 'nope' })).toEqual([]);
  });
});

describe('the filter in the URL', () => {
  it('reads known values and ignores others', () => {
    expect(
      parseStackFilter(new URLSearchParams('type=external&q=web')),
    ).toEqual({ type: 'external', q: 'web' });
    expect(parseStackFilter(new URLSearchParams('type=bogus'))).toEqual({
      type: 'all',
      q: '',
    });
  });

  it('writes only what differs from the default', () => {
    expect(stackFilterParams({ type: 'all', q: '' }).toString()).toBe('');
    expect(stackFilterParams({ type: 'managed', q: 'a b' }).toString()).toBe(
      'type=managed&q=a+b',
    );
  });

  it('knows when it filters', () => {
    expect(isFiltered({ type: 'all', q: '  ' })).toBe(false);
    expect(isFiltered({ type: 'external', q: '' })).toBe(true);
    expect(isFiltered({ type: 'all', q: 'x' })).toBe(true);
  });
});
