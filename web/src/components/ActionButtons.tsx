import DeleteSweepIcon from '@mui/icons-material/DeleteSweep';
import PlayArrowIcon from '@mui/icons-material/PlayArrow';
import RestartAltIcon from '@mui/icons-material/RestartAlt';
import StopIcon from '@mui/icons-material/Stop';
import SystemUpdateAltIcon from '@mui/icons-material/SystemUpdateAlt';
import { IconButton, Stack, Tooltip } from '@mui/material';
import { useState, type ReactElement } from 'react';
import { useTranslation } from 'react-i18next';
import type { StackAction, StackSummary } from '../api/client';
import { useRunAction } from '../api/queries';
import { actionEnabled, needsConfirmation, stackActions } from '../lib/actions';
import { errorMessage } from '../lib/errors';
import { ConfirmDialog } from './ConfirmDialog';
import { useNotify } from './Notifications';
import { useOperationPanel } from './OperationPanel';

const icons: Record<StackAction, ReactElement> = {
  start: <PlayArrowIcon />,
  stop: <StopIcon />,
  restart: <RestartAltIcon />,
  update: <SystemUpdateAltIcon />,
  down: <DeleteSweepIcon />,
};

type Props = {
  stack: Pick<StackSummary, 'name' | 'managed' | 'activeOperation'>;
};

/** The action buttons of a managed stack. */
export function ActionButtons({ stack }: Props) {
  const { t } = useTranslation();
  const notify = useNotify();
  const openPanel = useOperationPanel();
  const run = useRunAction();
  const [confirm, setConfirm] = useState<StackAction>();

  if (!stack.managed) return null;

  const start = (action: StackAction) =>
    run.mutate(
      { params: { path: { name: stack.name, action } } },
      {
        onSuccess: (op) => openPanel(op),
        onError: (e) => notify(errorMessage(t, e)),
      },
    );

  return (
    <Stack direction="row" sx={{ gap: 0.5 }}>
      {stackActions.map((action) => {
        const enabled = actionEnabled(stack, action) && !run.isPending;
        const hint = stack.activeOperation
          ? enabled
            ? t(`actions.hint.${action}`)
            : t('actions.busy')
          : t(`actions.hint.${action}`);
        return (
          <Tooltip key={action} title={`${t(`actions.${action}`)}: ${hint}`}>
            {/* span: tooltips need an enabled element to attach to */}
            <span>
              <IconButton
                size="small"
                aria-label={t(`actions.${action}`)}
                color={action === 'down' ? 'error' : 'default'}
                disabled={!enabled}
                onClick={() =>
                  needsConfirmation(action) ? setConfirm(action) : start(action)
                }
              >
                {icons[action]}
              </IconButton>
            </span>
          </Tooltip>
        );
      })}
      <ConfirmDialog
        open={confirm !== undefined}
        title={t('actions.confirmDownTitle', { name: stack.name })}
        text={t('actions.confirmDownText')}
        confirmLabel={t('actions.confirm')}
        onConfirm={() => confirm && start(confirm)}
        onClose={() => setConfirm(undefined)}
      />
    </Stack>
  );
}
