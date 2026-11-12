import ArrowBackIcon from '@mui/icons-material/ArrowBack';
import {
  Alert,
  Box,
  Button,
  Card,
  CardContent,
  Chip,
  CircularProgress,
  List,
  ListItemButton,
  ListItemText,
  Paper,
  Stack,
  Table,
  TableBody,
  TableCell,
  TableContainer,
  TableHead,
  TableRow,
  Typography,
} from '@mui/material';
import { Fragment } from 'react';
import { useTranslation } from 'react-i18next';
import { Link as RouterLink, useParams } from 'react-router';
import { apiError, type StackDetail } from '../api/client';
import { useStack, useStackOperations } from '../api/queries';
import { ActionButtons } from '../components/ActionButtons';
import { useOperationPanel } from '../components/OperationPanel';
import { OperationStatusChip, StatusChip } from '../components/StatusChip';
import { errorMessage } from '../lib/errors';
import { formatDateTime, formatDuration, formatPorts } from '../lib/format';
import { monoFont } from '../theme';

export function StackPage() {
  const { t } = useTranslation();
  const name = useParams().name ?? '';
  const stack = useStack(name);

  const back = (
    <Button
      component={RouterLink}
      to="/"
      startIcon={<ArrowBackIcon />}
      sx={{ alignSelf: 'flex-start' }}
    >
      {t('stack.back')}
    </Button>
  );

  if (stack.isPending) return <CircularProgress />;
  if (stack.isError) {
    const notFound = apiError(stack.error)?.code === 'not_found';
    return (
      <Stack sx={{ gap: 2 }}>
        {back}
        <Alert severity={notFound ? 'info' : 'error'}>
          {notFound
            ? t('stack.notFound', { name })
            : errorMessage(t, stack.error)}
        </Alert>
      </Stack>
    );
  }
  const s = stack.data;

  return (
    <Stack sx={{ gap: 2 }}>
      {back}
      <Stack
        direction="row"
        sx={{ gap: 1.5, alignItems: 'center', flexWrap: 'wrap' }}
      >
        <Typography variant="h4" component="h1">
          {s.name}
        </Typography>
        <StatusChip status={s.status} />
        <Chip
          size="small"
          variant="outlined"
          label={t(s.managed ? 'managed.managed' : 'managed.external')}
        />
        {s.activeOperation && <CircularProgress size={18} />}
        <Box sx={{ flex: 1 }} />
        <ActionButtons stack={s} />
      </Stack>
      <Typography variant="body2" color="text.secondary">
        {t('stack.path')}: {s.path} · {t('stacks.services')}:{' '}
        {s.servicesRunning}/{s.servicesTotal}
      </Typography>
      {!s.managed && <Alert severity="info">{t('managed.externalHint')}</Alert>}
      {s.warnings.map((w) => (
        <Alert key={w} severity="warning">
          {w}
        </Alert>
      ))}

      <Services stack={s} />
      <Operations stack={s.name} />
      <ComposeFile stack={s} />
    </Stack>
  );
}

function Services({ stack }: { stack: StackDetail }) {
  const { t } = useTranslation();
  return (
    <Box>
      <Typography variant="h6" component="h2" gutterBottom>
        {t('stack.servicesTitle')}
      </Typography>
      <TableContainer component={Paper} variant="outlined">
        <Table size="small">
          <TableHead>
            <TableRow>
              <TableCell>{t('stack.service')}</TableCell>
              <TableCell>{t('stack.container')}</TableCell>
              <TableCell>{t('stack.state')}</TableCell>
              <TableCell>{t('stack.health')}</TableCell>
              <TableCell>{t('stack.image')}</TableCell>
              <TableCell>{t('stack.ports')}</TableCell>
            </TableRow>
          </TableHead>
          <TableBody>
            {stack.services.map((svc) => (
              <Fragment key={svc.name}>
                {svc.containers.length === 0 ? (
                  <TableRow>
                    <TableCell sx={{ fontWeight: 600 }}>{svc.name}</TableCell>
                    <TableCell colSpan={5} sx={{ color: 'text.secondary' }}>
                      {t('stack.noContainers')}
                    </TableCell>
                  </TableRow>
                ) : (
                  svc.containers.map((c, i) => (
                    <TableRow key={c.id}>
                      <TableCell sx={{ fontWeight: 600 }}>
                        {i === 0 ? svc.name : ''}
                      </TableCell>
                      <TableCell>{c.name}</TableCell>
                      <TableCell>
                        <Chip
                          size="small"
                          color={c.state === 'running' ? 'success' : 'default'}
                          variant={
                            c.state === 'running' ? 'filled' : 'outlined'
                          }
                          label={c.state}
                        />
                        <Typography
                          variant="caption"
                          color="text.secondary"
                          sx={{ display: 'block' }}
                        >
                          {c.status}
                        </Typography>
                      </TableCell>
                      <TableCell>{t(`health.${c.health}`)}</TableCell>
                      <TableCell sx={{ fontFamily: monoFont, fontSize: 13 }}>
                        {c.image}
                      </TableCell>
                      <TableCell sx={{ fontFamily: monoFont, fontSize: 13 }}>
                        {formatPorts(c.ports).map((p) => (
                          <div key={p}>{p}</div>
                        ))}
                      </TableCell>
                    </TableRow>
                  ))
                )}
              </Fragment>
            ))}
          </TableBody>
        </Table>
      </TableContainer>
    </Box>
  );
}

function Operations({ stack }: { stack: string }) {
  const { t, i18n } = useTranslation();
  const operations = useStackOperations(stack);
  const openPanel = useOperationPanel();
  const list = operations.data?.operations ?? [];

  return (
    <Box>
      <Typography variant="h6" component="h2" gutterBottom>
        {t('stack.operations')}
      </Typography>
      <Paper variant="outlined">
        {list.length === 0 ? (
          <Typography sx={{ p: 2 }} color="text.secondary">
            {t('stack.noOperations')}
          </Typography>
        ) : (
          <List dense disablePadding>
            {list.slice(0, 10).map((op) => (
              <ListItemButton key={op.id} onClick={() => openPanel(op)}>
                <ListItemText
                  primary={t(`actions.${op.action}`)}
                  secondary={[
                    formatDateTime(op.startedAt, i18n.language),
                    op.finishedAt &&
                      formatDuration(
                        op.startedAt,
                        op.finishedAt,
                        i18n.language,
                      ),
                  ]
                    .filter(Boolean)
                    .join(' · ')}
                />
                <OperationStatusChip status={op.status} />
              </ListItemButton>
            ))}
          </List>
        )}
      </Paper>
    </Box>
  );
}

function ComposeFile({ stack }: { stack: StackDetail }) {
  const { t } = useTranslation();
  if (!stack.composeFile) return null;
  return (
    <Box>
      <Typography variant="h6" component="h2" gutterBottom>
        {t('stack.composeFile')}
      </Typography>
      <Card variant="outlined">
        <CardContent>
          <Typography
            variant="caption"
            color="text.secondary"
            sx={{ fontFamily: monoFont }}
          >
            {stack.composeFile.path}
          </Typography>
          {stack.composeFile.content === undefined ? (
            <Alert severity="info" sx={{ mt: 1 }}>
              {t('stack.composeUnreadable')}
            </Alert>
          ) : (
            <Box
              component="pre"
              sx={{
                m: 0,
                mt: 1,
                p: 1.5,
                overflow: 'auto',
                borderRadius: 1,
                bgcolor: 'action.hover',
                fontFamily: monoFont,
                fontSize: 13,
              }}
            >
              {stack.composeFile.content}
            </Box>
          )}
        </CardContent>
      </Card>
    </Box>
  );
}
