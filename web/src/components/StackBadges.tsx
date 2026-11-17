import { Chip, CircularProgress, Stack, Tooltip } from '@mui/material';
import { useTranslation } from 'react-i18next';
import type { StackSummary } from '../api/client';
import { StatusChip } from './StatusChip';

/** "Managed" or "External", the latter with an explanation on hover. */
export function TypeChip({ managed }: { managed: boolean }) {
  const { t } = useTranslation();
  if (managed) {
    return (
      <Chip size="small" variant="outlined" label={t('managed.managed')} />
    );
  }
  return (
    <Tooltip title={t('managed.externalHint')}>
      <Chip size="small" variant="outlined" label={t('managed.external')} />
    </Tooltip>
  );
}

/** The stack's status, with a spinner while an operation runs. */
export function StackStatus({
  stack,
}: {
  stack: Pick<StackSummary, 'status' | 'activeOperation'>;
}) {
  const { t } = useTranslation();
  return (
    <Stack direction="row" sx={{ gap: 1, alignItems: 'center' }}>
      <StatusChip status={stack.status} />
      {stack.activeOperation && (
        <Tooltip
          title={t('stacks.operationRunning', {
            action: t(`actions.${stack.activeOperation.action}`),
          })}
        >
          <CircularProgress size={16} />
        </Tooltip>
      )}
    </Stack>
  );
}
