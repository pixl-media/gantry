import { expect, it } from 'vitest';
import de from './locales/de.json';
import en from './locales/en.json';

function keys(obj: object, prefix = ''): string[] {
  return Object.entries(obj).flatMap(([k, v]) =>
    v && typeof v === 'object'
      ? keys(v as object, `${prefix}${k}.`)
      : [`${prefix}${k}`],
  );
}

it('German and English have the same keys', () => {
  expect(keys(de).sort()).toEqual(keys(en).sort());
});

it('no translation is empty', () => {
  for (const [lang, obj] of Object.entries({ en, de })) {
    for (const key of keys(obj)) {
      const value = key
        .split('.')
        .reduce<unknown>((o, k) => (o as Record<string, unknown>)[k], obj);
      expect(value, `${lang}: ${key}`).not.toBe('');
    }
  }
});
