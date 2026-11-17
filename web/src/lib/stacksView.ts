/** How the stack list is shown. */
export type StacksView = 'table' | 'cards';

// A personal preference, so it stays in this browser. Storage can be
// unavailable (private windows, blocked site data); then the default by
// screen width applies.
const key = 'gantry.stacksView';

export function storedStacksView(): StacksView | undefined {
  try {
    const v = localStorage.getItem(key);
    return v === 'table' || v === 'cards' ? v : undefined;
  } catch {
    return undefined;
  }
}

export function storeStacksView(view: StacksView) {
  try {
    localStorage.setItem(key, view);
  } catch {
    // Not stored; the choice holds until the page is left.
  }
}
