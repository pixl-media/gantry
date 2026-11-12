import { Chip, type ChipProps } from '@mui/material';
import { useTranslation } from 'react-i18next';
import type { OperationStatus, StackStatus } from '../api/client';

const stackColors: Record<StackStatus, ChipProps['color']> = {
  running: 'success',
  partial: 'warning',
  stopped: 'default',
  unknown: 'error',
};

export function StatusChip({ status }: { status: StackStatus }) {
  const { t } = useTranslation();
  return (
    <Chip
      size="small"
      color={stackColors[status]}
      variant={status === 'stopped' ? 'outlined' : 'filled'}
      label={t(`status.${status}`)}
    />
  );
}

const operationColors: Record<OperationStatus, ChipProps['color']> = {
  running: 'info',
  succeeded: 'success',
  failed: 'error',
  canceled: 'warning',
};

export function OperationStatusChip({ status }: { status: OperationStatus }) {
  const { t } = useTranslation();
  return (
    <Chip
      size="small"
      color={operationColors[status]}
      label={t(`operation.status.${status}`)}
    />
  );
}
