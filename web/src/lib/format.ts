import type { Container } from '../api/client';

export function formatDateTime(iso: string, language: string): string {
  return new Intl.DateTimeFormat(language, {
    dateStyle: 'short',
    timeStyle: 'medium',
  }).format(new Date(iso));
}

/** Duration between two timestamps, e.g. "3.2 s" or "1 min 4 s". */
export function formatDuration(
  fromIso: string,
  toIso: string,
  language: string,
): string {
  const ms = new Date(toIso).getTime() - new Date(fromIso).getTime();
  if (ms < 60_000) {
    const n = new Intl.NumberFormat(language, { maximumFractionDigits: 1 });
    return `${n.format(ms / 1000)} s`;
  }
  const minutes = Math.floor(ms / 60_000);
  const seconds = Math.round((ms % 60_000) / 1000);
  return `${minutes} min ${seconds} s`;
}

/** Published ports like "127.0.0.1:8081→80/tcp", unpublished ones as "80/tcp". */
export function formatPorts(ports: Container['ports']): string[] {
  const seen = new Set<string>();
  for (const p of ports) {
    const target = `${p.privatePort}/${p.type}`;
    seen.add(p.publicPort ? `${p.ip ?? ''}:${p.publicPort}→${target}` : target);
  }
  return [...seen];
}
