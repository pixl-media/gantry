import CloseIcon from '@mui/icons-material/Close';
import {
  Alert,
  Box,
  Button,
  CircularProgress,
  Drawer,
  IconButton,
  Stack,
  Typography,
} from '@mui/material';
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from 'react';
import { useTranslation } from 'react-i18next';
import type { Operation } from '../api/client';
import { useOperationEvents } from '../api/events';
import { useCancelOperation } from '../api/queries';
import { errorMessage } from '../lib/errors';
import { formatDateTime } from '../lib/format';
import { monoFont } from '../theme';
import { useNotify } from './Notifications';
import { OperationStatusChip } from './StatusChip';

type Target = Pick<Operation, 'id' | 'stack' | 'action' | 'startedAt'>;

const PanelContext = createContext<(op: Target) => void>(() => {});

/** Provides a side panel that shows an operation's live output. */
export function OperationPanelProvider({ children }: { children: ReactNode }) {
  const [target, setTarget] = useState<Target>();
  const open = useCallback((op: Target) => setTarget(op), []);
  return (
    <PanelContext.Provider value={open}>
      {children}
      <Drawer
        anchor="right"
        open={target !== undefined}
        onClose={() => setTarget(undefined)}
        slotProps={{ paper: { sx: { width: { xs: '100vw', sm: 640 } } } }}
      >
        {target && (
          <OperationView
            key={target.id}
            target={target}
            onClose={() => setTarget(undefined)}
          />
        )}
      </Drawer>
    </PanelContext.Provider>
  );
}

/** Opens the operation panel. */
export function useOperationPanel() {
  return useContext(PanelContext);
}

export function OperationView({
  target,
  onClose,
}: {
  target: Target;
  onClose: () => void;
}) {
  const { t, i18n } = useTranslation();
  const notify = useNotify();
  const { lines, final, reconnecting } = useOperationEvents(target.id);
  const cancel = useCancelOperation();
  const status = final?.status ?? 'running';

  // Follow new output while the user is at the bottom.
  const outputRef = useRef<HTMLPreElement>(null);
  const [atBottom, setAtBottom] = useState(true);
  useEffect(() => {
    const el = outputRef.current;
    if (el && atBottom) el.scrollTop = el.scrollHeight;
  }, [lines, atBottom]);

  return (
    <Stack sx={{ height: '100%' }}>
      <Stack
        direction="row"
        sx={{ alignItems: 'center', gap: 1.5, p: 2, pb: 1 }}
      >
        <Typography variant="h6" sx={{ flex: 1 }} noWrap>
          {t('operation.title', {
            action: t(`actions.${target.action}`),
            stack: target.stack,
          })}
        </Typography>
        {status === 'running' && <CircularProgress size={18} />}
        <OperationStatusChip status={status} />
        <IconButton aria-label={t('operation.close')} onClick={onClose}>
          <CloseIcon />
        </IconButton>
      </Stack>

      <Typography variant="body2" color="text.secondary" sx={{ px: 2 }}>
        {target.id} ·{' '}
        {t('operation.startedAt', {
          time: formatDateTime(target.startedAt, i18n.language),
        })}
        {final?.exitCode !== undefined &&
          ` · ${t('operation.exitCode', { code: final.exitCode })}`}
      </Typography>

      {final?.canceledBy && (
        <Alert severity="warning" sx={{ mx: 2, mt: 1 }}>
          {t('operation.canceledBy', { id: final.canceledBy })}
        </Alert>
      )}
      {final?.status === 'failed' && final.error && (
        <Alert severity="error" sx={{ mx: 2, mt: 1 }}>
          {final.error}
        </Alert>
      )}
      {reconnecting && (
        <Alert severity="info" sx={{ mx: 2, mt: 1 }}>
          {t('operation.reconnecting')}
        </Alert>
      )}

      <Box
        component="pre"
        ref={outputRef}
        data-testid="operation-output"
        onScroll={(e) => {
          const el = e.currentTarget;
          setAtBottom(el.scrollHeight - el.scrollTop - el.clientHeight < 24);
        }}
        sx={{
          flex: 1,
          m: 2,
          p: 1.5,
          overflow: 'auto',
          borderRadius: 1,
          bgcolor: 'grey.900',
          color: 'grey.100',
          fontFamily: monoFont,
          fontSize: 13,
          lineHeight: 1.5,
          whiteSpace: 'pre-wrap',
          wordBreak: 'break-all',
        }}
      >
        {lines.length > 0 ? lines.join('\n') : t('operation.waiting')}
      </Box>

      {status === 'running' && (
        <Box sx={{ px: 2, pb: 2 }}>
          <Button
            color="warning"
            variant="outlined"
            disabled={cancel.isPending}
            onClick={() =>
              cancel.mutate(
                { params: { path: { id: target.id } } },
                { onError: (e) => notify(errorMessage(t, e)) },
              )
            }
          >
            {t('operation.cancel')}
          </Button>
        </Box>
      )}
    </Stack>
  );
}
