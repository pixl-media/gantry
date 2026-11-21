// Set at build time (vite.config.ts): the release, the git version, or "dev".
declare const __GANTRY_VERSION__: string;

/** The web UI's own version. */
export const webVersion: string = __GANTRY_VERSION__;

/**
 * Whether two versions come from different releases: their major and minor
 * numbers differ ("0.2.0" and "v0.2.1-3-gabc" don't). Versions that aren't
 * numbered, such as "dev" or "edge", never count as different.
 */
export function differentReleases(a: string, b: string): boolean {
  const release = (v: string) =>
    /^v?(\d+)\.(\d+)/.exec(v)?.slice(1, 3).join('.');
  const ra = release(a);
  const rb = release(b);
  return ra !== undefined && rb !== undefined && ra !== rb;
}
