import {
  Alert,
  AlertTitle,
  Box,
  Chip,
  CircularProgress,
  Link,
  Paper,
  Stack,
  Table,
  TableBody,
  TableCell,
  TableContainer,
  TableHead,
  TableRow,
  Tooltip,
  Typography,
} from '@mui/material';
import { useTranslation } from 'react-i18next';
import { Link as RouterLink } from 'react-router';
import type { StackSummary } from '../api/client';
import { useStacks } from '../api/queries';
import { ActionButtons } from '../components/ActionButtons';
import { StatusChip } from '../components/StatusChip';
import { errorMessage } from '../lib/errors';

export function StacksPage() {
  const { t } = useTranslation();
  const stacks = useStacks();

  if (stacks.isPending) return <CircularProgress />;
  if (stacks.isError) {
    return <Alert severity="error">{errorMessage(t, stacks.error)}</Alert>;
  }
  const { stacks: list, dockerAvailable, warnings } = stacks.data;

  return (
    <Stack sx={{ gap: 2 }}>
      <Typography variant="h4" component="h1">
        {t('stacks.title')}
      </Typography>
      {!dockerAvailable && (
        <Alert severity="error">{t('stacks.dockerUnavailable')}</Alert>
      )}
      {warnings.length > 0 && (
        <Alert severity="warning">
          <AlertTitle>
            {t('stacks.warnings', { count: warnings.length })}
          </AlertTitle>
          {warnings.map((w) => (
            <div key={w}>{w}</div>
          ))}
        </Alert>
      )}
      {list.length === 0 ? (
        <Alert severity="info">{t('stacks.empty')}</Alert>
      ) : (
        <TableContainer component={Paper} variant="outlined">
          <Table size="small">
            <TableHead>
              <TableRow>
                <TableCell>{t('stacks.name')}</TableCell>
                <TableCell>{t('stacks.type')}</TableCell>
                <TableCell>{t('stacks.status')}</TableCell>
                <TableCell>{t('stacks.services')}</TableCell>
                <TableCell>{t('stacks.actions')}</TableCell>
              </TableRow>
            </TableHead>
            <TableBody>
              {list.map((s) => (
                <StackRow key={s.name} stack={s} />
              ))}
            </TableBody>
          </Table>
        </TableContainer>
      )}
    </Stack>
  );
}

function StackRow({ stack }: { stack: StackSummary }) {
  const { t } = useTranslation();
  return (
    <TableRow hover>
      {/* Long paths are cut off with "…"; the full path is in the tooltip. */}
      <TableCell sx={{ maxWidth: { xs: 160, md: 320 } }}>
        <Link
          component={RouterLink}
          to={`/stacks/${encodeURIComponent(stack.name)}`}
          sx={{ fontWeight: 600 }}
        >
          {stack.name}
        </Link>
        <Tooltip title={stack.path} placement="bottom-start">
          <Typography
            variant="caption"
            color="text.secondary"
            sx={{ display: 'block' }}
            noWrap
          >
            {stack.path}
          </Typography>
        </Tooltip>
      </TableCell>
      <TableCell>
        {stack.managed ? (
          <Chip size="small" variant="outlined" label={t('managed.managed')} />
        ) : (
          <Tooltip title={t('managed.externalHint')}>
            <Chip
              size="small"
              variant="outlined"
              label={t('managed.external')}
            />
          </Tooltip>
        )}
      </TableCell>
      <TableCell>
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
      </TableCell>
      <TableCell>
        {stack.servicesRunning}/{stack.servicesTotal}
      </TableCell>
      <TableCell>
        <Box>
          <ActionButtons stack={stack} />
        </Box>
      </TableCell>
    </TableRow>
  );
}
