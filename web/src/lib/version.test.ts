import { expect, it } from 'vitest';
import { differentReleases, webVersion } from './version';

it('compares major and minor numbers only', () => {
  expect(differentReleases('0.2.0', '0.2.1')).toBe(false);
  expect(differentReleases('0.2.0', 'v0.2.1-3-gabc1234')).toBe(false);
  expect(differentReleases('0.2.0', '0.3.0')).toBe(true);
  expect(differentReleases('1.2.0', '0.2.0')).toBe(true);
});

it('never calls versions without numbers different', () => {
  expect(differentReleases('dev', '0.2.0')).toBe(false);
  expect(differentReleases('0.2.0', 'edge')).toBe(false);
  expect(differentReleases('pr-38', '0.1.0')).toBe(false);
});

it('is "dev" outside a release build', () => {
  expect(webVersion).toBe('dev');
});
