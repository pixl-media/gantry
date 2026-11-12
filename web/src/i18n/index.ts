import i18n from 'i18next';
import LanguageDetector from 'i18next-browser-languagedetector';
import { initReactI18next } from 'react-i18next';
import de from './locales/de.json';
import en from './locales/en.json';

export const languages = ['en', 'de'] as const;
export type Language = (typeof languages)[number];

export const resources = {
  en: { translation: en },
  de: { translation: de },
} as const;

i18n
  .use(LanguageDetector)
  .use(initReactI18next)
  .init({
    resources,
    fallbackLng: 'en',
    supportedLngs: languages,
    nonExplicitSupportedLngs: true, // de-DE → de
    detection: {
      // Only the chosen language is stored, never anything sensitive.
      order: ['localStorage', 'navigator'],
      caches: ['localStorage'],
      lookupLocalStorage: 'gantry.language',
    },
    interpolation: { escapeValue: false }, // React escapes already
  });

i18n.on('languageChanged', (lng) => {
  document.documentElement.lang = lng;
});

export default i18n;
