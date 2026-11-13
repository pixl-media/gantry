import type { TFunction } from 'i18next';
import { apiError } from '../api/client';

const translated = [
  'stack_busy',
  'stack_unmanaged',
  'operation_finished',
  'not_found',
  'origin_rejected',
  'unauthorized',
  'version_conflict',
  'stack_exists',
] as const;

type TranslatedCode = (typeof translated)[number];

function isTranslated(code: string | undefined): code is TranslatedCode {
  return (translated as readonly string[]).includes(code ?? '');
}

/** A user-facing message for a failed request, in the active language. */
export function errorMessage(t: TFunction, error: unknown): string {
  const e = apiError(error);
  if (isTranslated(e?.code)) {
    return t(`errors.${e.code}`);
  }
  const message =
    e?.error ?? (error instanceof Error ? error.message : String(error));
  return t('errors.generic', { message });
}
