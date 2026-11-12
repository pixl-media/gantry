import { act, screen } from '@testing-library/react';
import { afterEach, expect, it } from 'vitest';
import i18n from '../i18n';
import { renderWithProviders } from '../test/render';
import { OperationStatusChip, StatusChip } from './StatusChip';

afterEach(async () => {
  await i18n.changeLanguage('en');
});

it('shows the stack status in the active language', async () => {
  renderWithProviders(<StatusChip status="partial" />);
  expect(screen.getByText('Partly running')).toBeInTheDocument();

  await act(() => i18n.changeLanguage('de'));
  expect(screen.getByText('Läuft teilweise')).toBeInTheDocument();
});

it('shows operation statuses', () => {
  renderWithProviders(<OperationStatusChip status="canceled" />);
  expect(screen.getByText('Canceled')).toBeInTheDocument();
});
