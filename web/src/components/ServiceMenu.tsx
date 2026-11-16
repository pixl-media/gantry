import MoreVertIcon from '@mui/icons-material/MoreVert';
import PlayArrowIcon from '@mui/icons-material/PlayArrow';
import RestartAltIcon from '@mui/icons-material/RestartAlt';
import StopIcon from '@mui/icons-material/Stop';
import SystemUpdateAltIcon from '@mui/icons-material/SystemUpdateAlt';
import {
  IconButton,
  ListItemIcon,
  ListItemText,
  Menu,
  MenuItem,
  Tooltip,
} from '@mui/material';
import { useState, type ReactElement } from 'react';
import { useTranslation } from 'react-i18next';
import {
  apiError,
  type Service,
  type ServiceAction,
  type ServiceActionPlan,
  type StackSummary,
} from '../api/client';
import { useRunServiceAction } from '../api/queries';
import { serviceActionEnabled, serviceActions } from '../lib/actions';
import { errorMessage } from '../lib/errors';
import { useNotify } from './Notifications';
import { useOperationPanel } from './OperationPanel';
import { ServiceActionDialog } from './ServiceActionDialog';

const icons: Record<ServiceAction, ReactElement> = {
  start: <PlayArrowIcon fontSize="small" />,
  stop: <StopIcon fontSize="small" />,
  restart: <RestartAltIcon fontSize="small" />,
  update: <SystemUpdateAltIcon fontSize="small" />,
};

type Props = {
  stack: Pick<StackSummary, 'name' | 'managed' | 'activeOperation'>;
  service: Service;
};

/**
 * The actions of one service of a managed stack, in a menu. If an action
 * changes other services too, the agent sends the plan first, which is shown
 * for confirmation.
 */
export function ServiceMenu({ stack, service }: Props) {
  const { t } = useTranslation();
  const notify = useNotify();
  const openPanel = useOperationPanel();
  const run = useRunServiceAction();
  const [anchor, setAnchor] = useState<HTMLElement | null>(null);
  const [plan, setPlan] = useState<ServiceActionPlan>();
  const [changed, setChanged] = useState(false);

  if (!stack.managed) return null;

  const start = (action: ServiceAction, confirm?: string[]) =>
    run.mutate(
      {
        params: { path: { name: stack.name, service: service.name, action } },
        body: confirm ? { confirm } : undefined,
      },
      {
        onSuccess: (op) => {
          setPlan(undefined);
          openPanel(op);
        },
        onError: (e) => {
          const err = apiError(e);
          if (err?.code === 'confirmation_required' && err.plan) {
            setChanged(confirm !== undefined);
            setPlan(err.plan);
            return;
          }
          setPlan(undefined);
          notify(errorMessage(t, e));
        },
      },
    );

  const busy = Boolean(stack.activeOperation);
  return (
    <>
      <Tooltip title={busy ? t('service.busy') : ''}>
        {/* span: tooltips need an enabled element to attach to */}
        <span>
          <IconButton
            size="small"
            aria-label={t('service.actions', { name: service.name })}
            aria-haspopup="menu"
            disabled={busy || run.isPending}
            onClick={(e) => setAnchor(e.currentTarget)}
          >
            <MoreVertIcon fontSize="small" />
          </IconButton>
        </span>
      </Tooltip>
      <Menu
        anchorEl={anchor}
        open={anchor !== null}
        onClose={() => setAnchor(null)}
      >
        {serviceActions.map((action) => (
          <MenuItem
            key={action}
            disabled={!serviceActionEnabled(stack, service, action)}
            sx={{ whiteSpace: 'normal', maxWidth: 360 }}
            onClick={() => {
              setAnchor(null);
              start(action);
            }}
          >
            <ListItemIcon>{icons[action]}</ListItemIcon>
            <ListItemText
              primary={t(`actions.${action}`)}
              secondary={t(`service.hint.${action}`)}
            />
          </MenuItem>
        ))}
      </Menu>
      <ServiceActionDialog
        plan={plan}
        changed={changed}
        pending={run.isPending}
        onConfirm={(p) =>
          start(
            p.action,
            p.affected.map((a) => a.service),
          )
        }
        onClose={() => setPlan(undefined)}
      />
    </>
  );
}
