import i18next from 'i18next';
import LanguageDetector from 'i18next-browser-languagedetector';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { options } from '.';

// A fresh instance per test: the shared one is set to English for all tests.
async function startLanguage() {
  const i18n = i18next.createInstance().use(LanguageDetector);
  await i18n.init(options);
  return i18n.resolvedLanguage;
}

// The shared instance stored its language already.
beforeEach(() => {
  localStorage.clear();
});

afterEach(() => {
  vi.restoreAllMocks();
});

it('starts in English, whatever the browser language', async () => {
  vi.spyOn(navigator, 'language', 'get').mockReturnValue('de-DE');
  vi.spyOn(navigator, 'languages', 'get').mockReturnValue(['de-DE', 'de']);
  expect(await startLanguage()).toBe('en');
});

it('keeps a language chosen before', async () => {
  localStorage.setItem('gantry.language', 'de');
  expect(await startLanguage()).toBe('de');
});
