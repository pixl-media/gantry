import { describe, expect, it } from 'vitest';
import { color256, parseAnsi, stripAnsi } from './ansi';

describe('parseAnsi', () => {
  it('returns plain text unchanged', () => {
    expect(parseAnsi('hello world')).toEqual([
      { text: 'hello world', style: {} },
    ]);
  });

  it('applies colors and resets', () => {
    const segs = parseAnsi('a \x1b[31mred\x1b[0m b');
    expect(segs.map((s) => s.text)).toEqual(['a ', 'red', ' b']);
    expect(segs[1]!.style.color).toBeDefined();
    expect(segs[0]!.style).toEqual({});
    expect(segs[2]!.style).toEqual({});
  });

  it('combines bold with a bright color, and ends bold with 22', () => {
    const segs = parseAnsi('\x1b[1;92mok\x1b[22m still green');
    expect(segs[0]!.style.bold).toBe(true);
    expect(segs[0]!.style.color).toBeDefined();
    expect(segs[1]!.style.bold).toBe(false);
    expect(segs[1]!.style.color).toBe(segs[0]!.style.color);
  });

  it('supports 256 and 24-bit colors', () => {
    expect(parseAnsi('\x1b[38;5;196mx')[0]!.style.color).toBe(color256(196));
    expect(parseAnsi('\x1b[48;2;10;20;30mx')[0]!.style.background).toBe(
      'rgb(10, 20, 30)',
    );
  });

  it('treats an empty SGR as reset', () => {
    const segs = parseAnsi('\x1b[33my\x1b[mz');
    expect(segs[1]!.style).toEqual({});
  });

  it('removes other escape sequences and control characters', () => {
    const line =
      '\x1b]0;window title\x07\x1b[2K\x1b[1Gprogress\x1b[?25l done\x08\x00\ttab';
    expect(stripAnsi(line)).toBe('progress done\ttab');
  });
});

describe('color256', () => {
  it('covers the palette ranges', () => {
    expect(color256(1)).toMatch(/^#/);
    expect(color256(9)).toMatch(/^#/);
    expect(color256(16)).toBe('rgb(0, 0, 0)');
    expect(color256(231)).toBe('rgb(255, 255, 255)');
    expect(color256(232)).toBe('rgb(8, 8, 8)');
    expect(color256(256)).toBeUndefined();
  });
});
