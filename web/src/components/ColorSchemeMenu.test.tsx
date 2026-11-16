import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it } from 'vitest';
import { renderWithProviders } from '../test/render';
import { ColorSchemeMenu } from './ColorSchemeMenu';

afterEach(() => {
  localStorage.clear();
});

describe('ColorSchemeMenu', () => {
  it('switches to dark and remembers the choice', async () => {
    renderWithProviders(<ColorSchemeMenu />);
    await userEvent.click(screen.getByRole('button', { name: 'Appearance' }));
    expect(screen.getByRole('menuitem', { name: 'System' })).toHaveClass(
      'Mui-selected',
    );

    await userEvent.click(screen.getByRole('menuitem', { name: 'Dark' }));

    await waitFor(() =>
      expect(
        document.documentElement.getAttribute('data-mui-color-scheme'),
      ).toBe('dark'),
    );
    expect(localStorage.getItem('mui-mode')).toBe('dark');
  });

  it('switches back to light', async () => {
    localStorage.setItem('mui-mode', 'dark');
    renderWithProviders(<ColorSchemeMenu />);
    await userEvent.click(screen.getByRole('button', { name: 'Appearance' }));
    await userEvent.click(screen.getByRole('menuitem', { name: 'Light' }));

    await waitFor(() =>
      expect(
        document.documentElement.getAttribute('data-mui-color-scheme'),
      ).toBe('light'),
    );
    expect(localStorage.getItem('mui-mode')).toBe('light');
  });
});
