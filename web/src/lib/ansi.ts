/**
 * A small parser for ANSI escape codes in log lines. Color and text style
 * codes (SGR) become styled segments; every other control sequence (cursor
 * movement, clearing, window titles, …) is removed.
 */

export interface AnsiStyle {
  color?: string;
  background?: string;
  bold?: boolean;
  dim?: boolean;
  italic?: boolean;
  underline?: boolean;
}

export interface Segment {
  text: string;
  style: AnsiStyle;
}

// Colors for the dark log background, close to common terminal themes.
const basic = [
  '#4e4e4e', // black (lightened so it's visible on dark backgrounds)
  '#f2777a', // red
  '#99cc99', // green
  '#ffcc66', // yellow
  '#6699cc', // blue
  '#cc99cc', // magenta
  '#66cccc', // cyan
  '#d3d0c8', // white
];
const bright = [
  '#747369',
  '#ff9da4',
  '#c2f0a8',
  '#ffe08a',
  '#99c2ff',
  '#e8b3ff',
  '#99ffff',
  '#ffffff',
];

/** The color of an index in the 256-color palette. */
export function color256(n: number): string | undefined {
  if (n < 0 || n > 255 || !Number.isInteger(n)) return undefined;
  if (n < 8) return basic[n];
  if (n < 16) return bright[n - 8];
  if (n < 232) {
    const i = n - 16;
    const level = (v: number) => (v === 0 ? 0 : 55 + v * 40);
    return `rgb(${level(Math.floor(i / 36))}, ${level(Math.floor(i / 6) % 6)}, ${level(i % 6)})`;
  }
  const gray = 8 + (n - 232) * 10;
  return `rgb(${gray}, ${gray}, ${gray})`;
}

// eslint-disable-next-line no-control-regex
const csi = /\x1b\[([0-9;:?]*)([@-~])/g;
// eslint-disable-next-line no-control-regex
const osc = /\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)/g;
// eslint-disable-next-line no-control-regex
const otherEscape = /\x1b[@-Z\\-_]/g;
// Control characters except tab; ESC is handled above.
// eslint-disable-next-line no-control-regex
const controls = /[\x00-\x08\x0b-\x1a\x1c-\x1f\x7f]/g;

function clean(text: string): string {
  return text.replace(otherEscape, '').replace(controls, '');
}

/** Applies the parameters of one SGR sequence to a style. */
function applySgr(params: string, style: AnsiStyle): AnsiStyle {
  const codes = params === '' ? [0] : params.split(/[;:]/).map(Number);
  let s = { ...style };
  for (let i = 0; i < codes.length; i++) {
    const c = codes[i]!;
    if (c === 0) s = {};
    else if (c === 1) s.bold = true;
    else if (c === 2) s.dim = true;
    else if (c === 3) s.italic = true;
    else if (c === 4) s.underline = true;
    else if (c === 22) {
      s.bold = false;
      s.dim = false;
    } else if (c === 23) s.italic = false;
    else if (c === 24) s.underline = false;
    else if (c >= 30 && c <= 37) s.color = basic[c - 30];
    else if (c === 39) delete s.color;
    else if (c >= 40 && c <= 47) s.background = basic[c - 40];
    else if (c === 49) delete s.background;
    else if (c >= 90 && c <= 97) s.color = bright[c - 90];
    else if (c >= 100 && c <= 107) s.background = bright[c - 100];
    else if (c === 38 || c === 48) {
      // Extended colors: 5;n (256 colors) or 2;r;g;b (24 bit).
      const key = c === 38 ? 'color' : 'background';
      if (codes[i + 1] === 5) {
        const col = color256(codes[i + 2]!);
        if (col) s[key] = col;
        i += 2;
      } else if (codes[i + 1] === 2) {
        const [r, g, b] = [codes[i + 2], codes[i + 3], codes[i + 4]];
        if ([r, g, b].every((v) => v !== undefined && v >= 0 && v <= 255)) {
          s[key] = `rgb(${r}, ${g}, ${b})`;
        }
        i += 4;
      }
    }
  }
  return s;
}

/** Splits a line into styled segments, without any escape codes. */
export function parseAnsi(line: string): Segment[] {
  const input = line.replace(osc, '');
  const segments: Segment[] = [];
  let style: AnsiStyle = {};
  let last = 0;
  const push = (text: string) => {
    const t = clean(text);
    if (t) segments.push({ text: t, style });
  };
  for (const m of input.matchAll(csi)) {
    push(input.slice(last, m.index));
    if (m[2] === 'm') style = applySgr(m[1]!, style);
    last = m.index + m[0].length;
  }
  push(input.slice(last));
  return segments;
}

/** The plain text of a line, without escape codes (for search and download). */
export function stripAnsi(line: string): string {
  return parseAnsi(line)
    .map((s) => s.text)
    .join('');
}
