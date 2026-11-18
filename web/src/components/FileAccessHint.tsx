import ContentCopyIcon from '@mui/icons-material/ContentCopy';
import {
  Alert,
  AlertTitle,
  Box,
  IconButton,
  Stack,
  Tooltip,
  Typography,
} from '@mui/material';
import { useTranslation } from 'react-i18next';
import type { StackDetail } from '../api/client';
import { chownCommand } from '../lib/fileAccess';
import { monoFont } from '../theme';
import { useNotify } from './Notifications';

/**
 * What the agent may not read or write for a managed stack, and the command
 * that allows it. gantry never changes owners itself. A warning if files
 * can't be read (then Compose fails too), otherwise a note: controlling a
 * stack without changing its files is fine.
 */
export function FileAccessHint({ stack }: { stack: StackDetail }) {
  const { t } = useTranslation();
  const notify = useNotify();
  const access = stack.fileAccess;
  if (!stack.managed || !access || access.missing.length === 0) return null;

  const command = chownCommand(access);
  const readOnlyFs = access.missing.some((m) => m.readOnlyFilesystem);
  const cantRead = access.missing.some((m) => m.access === 'read');
  // Not available over plain HTTP from another host (no secure context);
  // the command can still be selected by hand.
  const canCopy = typeof navigator.clipboard?.writeText === 'function';

  return (
    <Alert severity={cantRead ? 'warning' : 'info'}>
      <AlertTitle>{t('access.title')}</AlertTitle>
      <Stack sx={{ gap: 1 }}>
        <Typography variant="body2">
          {t('access.intro', { user: access.user })}
        </Typography>
        <Box component="ul" sx={{ m: 0, pl: 2.5 }}>
          {access.missing.map((m) => (
            <li key={`${m.access}:${m.path}`}>
              <Typography variant="body2">
                {t(m.access === 'write' ? 'access.write' : 'access.read', {
                  path: m.path,
                })}
                {m.readOnlyFilesystem && ` ${t('access.readOnlyFs')}`}
              </Typography>
            </li>
          ))}
        </Box>
        {!stack.editable ? (
          <Typography variant="body2">{t('access.notEditable')}</Typography>
        ) : (
          !stack.deletable && (
            <Typography variant="body2">{t('access.notDeletable')}</Typography>
          )
        )}
        {command && (
          <>
            <Typography variant="body2">{t('access.fix')}</Typography>
            <Stack direction="row" sx={{ alignItems: 'center', gap: 1 }}>
              <Box
                component="code"
                data-testid="chown-command"
                sx={{
                  fontFamily: monoFont,
                  fontSize: '0.85rem',
                  px: 1,
                  py: 0.5,
                  borderRadius: 1,
                  bgcolor: 'action.hover',
                  wordBreak: 'break-all',
                  userSelect: 'all',
                }}
              >
                {command}
              </Box>
              {canCopy && (
                <Tooltip title={t('access.copy')}>
                  <IconButton
                    size="small"
                    aria-label={t('access.copy')}
                    onClick={() =>
                      navigator.clipboard
                        .writeText(command)
                        .then(() => notify(t('access.copied'), 'success'))
                        .catch(() => {})
                    }
                  >
                    <ContentCopyIcon fontSize="small" />
                  </IconButton>
                </Tooltip>
              )}
            </Stack>
          </>
        )}
        {readOnlyFs && (
          <Typography variant="body2">{t('access.readOnlyFsHint')}</Typography>
        )}
      </Stack>
    </Alert>
  );
}
