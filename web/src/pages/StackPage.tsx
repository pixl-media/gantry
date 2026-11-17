import ArrowBackIcon from '@mui/icons-material/ArrowBack';
import ArticleIcon from '@mui/icons-material/Article';
import DeleteForeverIcon from '@mui/icons-material/DeleteForever';
import MoreVertIcon from '@mui/icons-material/MoreVert';
import {
  Alert,
  Box,
  Button,
  Chip,
  CircularProgress,
  IconButton,
  List,
  ListItemButton,
  ListItemIcon,
  ListItemText,
  Menu,
  MenuItem,
  Paper,
  Stack,
  Tab,
  Table,
  TableBody,
  TableCell,
  TableContainer,
  TableHead,
  TableRow,
  Tabs,
  Tooltip,
  Typography,
} from '@mui/material';
import { Fragment, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Link as RouterLink,
  useLocation,
  useParams,
  useSearchParams,
} from 'react-router';
import { apiError, type StackDetail } from '../api/client';
import { useStack, useStackOperations } from '../api/queries';
import { ActionButtons } from '../components/ActionButtons';
import { ComposeTab } from '../components/ComposeTab';
import { DeleteStackDialog } from '../components/DeleteStackDialog';
import { EnvTab } from '../components/EnvTab';
import { LogView } from '../components/LogView';
import { useOperationPanel } from '../components/OperationPanel';
import { ServiceMenu } from '../components/ServiceMenu';
import { OperationStatusChip, StatusChip } from '../components/StatusChip';
import { errorMessage } from '../lib/errors';
import { formatDateTime, formatDuration, formatPorts } from '../lib/format';
import { monoFont } from '../theme';

export function StackPage() {
  const { t } = useTranslation();
  const name = useParams().name ?? '';
  const stack = useStack(name);
  // The tab and the log filter live in the URL, so they can be linked.
  const [searchParams, setSearchParams] = useSearchParams();
  const requested = searchParams.get('tab');
  const logContainer = searchParams.get('container') ?? undefined;
  const [menu, setMenu] = useState<HTMLElement | null>(null);
  const [deleting, setDeleting] = useState(false);
  // The list's filter, passed along by its links, for the way back. Kept
  // from the first render: switching tabs replaces the location's state.
  const location = useLocation();
  const [listSearch] = useState(() => {
    const s = (location.state as { listSearch?: unknown } | null)?.listSearch;
    return typeof s === 'string' && s.startsWith('?') ? s : '';
  });

  const back = (
    <Button
      component={RouterLink}
      to={'/' + listSearch}
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
  const tabs = ['overview', 'logs', 'compose', ...(s.managed ? ['env'] : [])];
  const tab = tabs.includes(requested ?? '') ? requested! : 'overview';

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
        {s.managed && (
          <>
            <Tooltip title={t('stack.moreActions')}>
              <IconButton
                aria-label={t('stack.moreActions')}
                onClick={(e) => setMenu(e.currentTarget)}
              >
                <MoreVertIcon />
              </IconButton>
            </Tooltip>
            <Menu
              anchorEl={menu}
              open={menu !== null}
              onClose={() => setMenu(null)}
            >
              <MenuItem
                sx={{ color: 'error.main' }}
                onClick={() => {
                  setMenu(null);
                  setDeleting(true);
                }}
              >
                <ListItemIcon sx={{ color: 'inherit' }}>
                  <DeleteForeverIcon fontSize="small" />
                </ListItemIcon>
                {t('stack.deleteStack')}
              </MenuItem>
            </Menu>
            <DeleteStackDialog
              open={deleting}
              onClose={() => setDeleting(false)}
              stack={s}
            />
          </>
        )}
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

      <Tabs
        value={tab}
        onChange={(_, v: string) =>
          setSearchParams(v === 'overview' ? {} : { tab: v })
        }
        variant="scrollable"
        allowScrollButtonsMobile
        sx={{ borderBottom: 1, borderColor: 'divider' }}
      >
        <Tab value="overview" label={t('stack.tabOverview')} />
        <Tab value="logs" label={t('stack.tabLogs')} />
        <Tab value="compose" label={t('stack.tabCompose')} />
        {s.managed && <Tab value="env" label={t('stack.tabEnv')} />}
      </Tabs>

      {tab === 'logs' && (
        <LogView
          stack={s.name}
          services={s.services.map((svc) => svc.name)}
          container={logContainer}
          onContainerChange={(c) =>
            setSearchParams(c ? { tab: 'logs', container: c } : { tab: 'logs' })
          }
        />
      )}
      {tab === 'compose' && <ComposeTab stack={s} />}
      {tab === 'env' && <EnvTab stack={s.name} />}
      {tab === 'overview' && (
        <>
          <Services
            stack={s}
            onShowLogs={(c) => setSearchParams({ tab: 'logs', container: c })}
          />
          <Operations stack={s.name} />
        </>
      )}
    </Stack>
  );
}

function Services({
  stack,
  onShowLogs,
}: {
  stack: StackDetail;
  onShowLogs: (container: string) => void;
}) {
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
              <TableCell />
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
                    <TableCell padding="none" align="right">
                      <ServiceMenu stack={stack} service={svc} />
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
                      <TableCell padding="none" align="right">
                        <Stack
                          direction="row"
                          sx={{ justifyContent: 'flex-end', px: 0.5 }}
                        >
                          <Tooltip title={t('stack.showLogs')}>
                            <IconButton
                              size="small"
                              aria-label={t('stack.showLogsOf', {
                                name: c.name,
                              })}
                              onClick={() => onShowLogs(c.name)}
                            >
                              <ArticleIcon fontSize="small" />
                            </IconButton>
                          </Tooltip>
                          {i === 0 && (
                            <ServiceMenu stack={stack} service={svc} />
                          )}
                        </Stack>
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
                  primary={
                    op.service
                      ? `${t(`actions.${op.action}`)} · ${op.service}`
                      : t(`actions.${op.action}`)
                  }
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
