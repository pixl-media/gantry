import type { FileAccess } from '../api/client';

function shellQuote(s: string): string {
  return /^[\w./-]+$/.test(s) ? s : `'${s.replaceAll("'", `'\\''`)}'`;
}

/**
 * The command that gives the agent's user the missing access: a change of
 * owner for exactly these paths, never recursive, so the data of the stack's
 * containers keeps its owner. Undefined if an owner change can't help (only
 * paths on read-only file systems) or nothing is missing.
 */
export function chownCommand(access: FileAccess): string | undefined {
  const paths = access.missing
    .filter((m) => !m.readOnlyFilesystem)
    .map((m) => shellQuote(m.path));
  if (paths.length === 0) return undefined;
  return `sudo chown ${shellQuote(access.user)} ${[...new Set(paths)].join(' ')}`;
}
