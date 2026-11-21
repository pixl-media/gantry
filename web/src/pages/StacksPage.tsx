import AddIcon from '@mui/icons-material/Add';
import GridViewIcon from '@mui/icons-material/GridView';
import SearchIcon from '@mui/icons-material/Search';
import ViewListIcon from '@mui/icons-material/ViewList';
import {
  Alert,
  AlertTitle,
  Box,
  Button,
  CircularProgress,
  InputAdornment,
  Link,
  Paper,
  Stack,
  Table,
  TableBody,
  TableCell,
  TableContainer,
  TableHead,
  TableRow,
  TextField,
  ToggleButton,
  ToggleButtonGroup,
  Tooltip,
  Typography,
  useMediaQuery,
  useTheme,
} from '@mui/material';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link as RouterLink, useLocation, useSearchParams } from 'react-router';
import type { StackSummary } from '../api/client';
import { useStacks } from '../api/queries';
import { ActionButtons } from '../components/ActionButtons';
import { CreateStackDialog } from '../components/CreateStackDialog';
import { StackStatus, TypeChip } from '../components/StackBadges';
import { StackCard } from '../components/StackCard';
import { errorMessage } from '../lib/errors';
import { chownCommand } from '../lib/fileAccess';
import {
  filterStacks,
  isFiltered,
  parseStackFilter,
  stackFilterParams,
  type StackFilter,
  type StackTypeFilter,
} from '../lib/stackFilter';
import {
  storedStacksView,
  storeStacksView,
  type StacksView,
} from '../lib/stacksView';

export function StacksPage() {
  const { t } = useTranslation();
  const stacks = useStacks();
  const [creating, setCreating] = useState(false);
  // The filter lives in the URL, so it survives going to a stack and back.
  const [params, setParams] = useSearchParams();
  const filter = parseStackFilter(params);
  const listSearch = useLocation().search;
  const setFilter = (change: Partial<StackFilter>) =>
    setParams(stackFilterParams({ ...filter, ...change }), { replace: true });
  // Cards on phones, the table elsewhere, unless the user chose.
  const small = useMediaQuery(useTheme().breakpoints.down('sm'));
  const [chosenView, setChosenView] = useState(storedStacksView);
  const view = chosenView ?? (small ? 'cards' : 'table');
  const chooseView = (v: StacksView) => {
    setChosenView(v);
    storeStacksView(v);
  };

  if (stacks.isPending) return <CircularProgress />;
  if (stacks.isError) {
    return <Alert severity="error">{errorMessage(t, stacks.error)}</Alert>;
  }
  const { stacks: list, dockerAvailable, warnings, fileAccess } = stacks.data;
  // Optional for an agent older than the UI.
  const createBlocked = fileAccess?.missing[0];
  const createCommand = fileAccess && chownCommand(fileAccess);
  const shown = filterStacks(list, filter);

  return (
    <Stack sx={{ gap: 2 }}>
      <Stack direction="row" sx={{ alignItems: 'center', gap: 2 }}>
        <Stack
          direction="row"
          sx={{ flex: 1, alignItems: 'baseline', gap: 1.5, minWidth: 0 }}
        >
          <Typography variant="h4" component="h1">
            {t('stacks.title')}
          </Typography>
          {isFiltered(filter) && (
            <Typography color="textSecondary">
              {t('stacks.filtered', {
                shown: shown.length,
                total: list.length,
              })}
            </Typography>
          )}
        </Stack>
        <Tooltip
          title={
            createBlocked
              ? createCommand
                ? t('access.cannotCreate', {
                    user: fileAccess.user,
                    path: createBlocked.path,
                    command: createCommand,
                  })
                : t('access.cannotCreateReadOnly', { path: createBlocked.path })
              : ''
          }
        >
          {/* A span, so the tooltip also shows on the disabled button. */}
          <span>
            <Button
              variant="contained"
              startIcon={<AddIcon />}
              disabled={createBlocked !== undefined}
              onClick={() => setCreating(true)}
            >
              {t('create.button')}
            </Button>
          </span>
        </Tooltip>
      </Stack>
      <CreateStackDialog
        open={creating}
        onClose={() => setCreating(false)}
        existing={list.map((s) => s.name)}
      />
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
        <>
          <Stack
            direction="row"
            sx={{ gap: 1.5, alignItems: 'center', flexWrap: 'wrap' }}
          >
            <ToggleButtonGroup
              size="small"
              exclusive
              value={filter.type}
              onChange={(_, v: StackTypeFilter | null) =>
                v && setFilter({ type: v })
              }
              aria-label={t('stacks.type')}
            >
              <ToggleButton value="all">{t('stacks.filterAll')}</ToggleButton>
              <ToggleButton value="managed">
                {t('managed.managed')}
              </ToggleButton>
              <ToggleButton value="external">
                {t('managed.external')}
              </ToggleButton>
            </ToggleButtonGroup>
            <TextField
              size="small"
              type="search"
              placeholder={t('stacks.search')}
              value={filter.q}
              onChange={(e) => setFilter({ q: e.target.value })}
              sx={{ flex: 1, minWidth: 160, maxWidth: 320 }}
              slotProps={{
                htmlInput: { 'aria-label': t('stacks.search') },
                input: {
                  startAdornment: (
                    <InputAdornment position="start">
                      <SearchIcon fontSize="small" />
                    </InputAdornment>
                  ),
                },
              }}
            />
            <ToggleButtonGroup
              size="small"
              exclusive
              value={view}
              onChange={(_, v: StacksView | null) => v && chooseView(v)}
              sx={{ ml: 'auto' }}
            >
              <ToggleButton value="table" aria-label={t('stacks.viewTable')}>
                <Tooltip title={t('stacks.viewTable')}>
                  <ViewListIcon fontSize="small" />
                </Tooltip>
              </ToggleButton>
              <ToggleButton value="cards" aria-label={t('stacks.viewCards')}>
                <Tooltip title={t('stacks.viewCards')}>
                  <GridViewIcon fontSize="small" />
                </Tooltip>
              </ToggleButton>
            </ToggleButtonGroup>
          </Stack>
          {shown.length === 0 ? (
            <Alert
              severity="info"
              action={
                <Button
                  color="inherit"
                  size="small"
                  onClick={() => setParams({}, { replace: true })}
                >
                  {t('stacks.resetFilter')}
                </Button>
              }
            >
              {t('stacks.noMatch')}
            </Alert>
          ) : view === 'cards' ? (
            <Box
              sx={{
                display: 'grid',
                gap: 2,
                gridTemplateColumns: 'repeat(auto-fill, minmax(280px, 1fr))',
              }}
            >
              {shown.map((s) => (
                <StackCard key={s.name} stack={s} listSearch={listSearch} />
              ))}
            </Box>
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
                  {shown.map((s) => (
                    <StackRow key={s.name} stack={s} listSearch={listSearch} />
                  ))}
                </TableBody>
              </Table>
            </TableContainer>
          )}
        </>
      )}
    </Stack>
  );
}

function StackRow({
  stack,
  listSearch,
}: {
  stack: StackSummary;
  listSearch: string;
}) {
  return (
    <TableRow hover>
      {/* Long paths are cut off with "…"; the full path is in the tooltip. */}
      <TableCell sx={{ maxWidth: { xs: 160, md: 320 } }}>
        <Link
          component={RouterLink}
          to={`/stacks/${encodeURIComponent(stack.name)}`}
          state={{ listSearch }}
          sx={{ fontWeight: 600 }}
        >
          {stack.name}
        </Link>
        <Tooltip title={stack.path} placement="bottom-start">
          <Typography
            variant="caption"
            color="textSecondary"
            sx={{ display: 'block' }}
            noWrap
          >
            {stack.path}
          </Typography>
        </Tooltip>
      </TableCell>
      <TableCell>
        <TypeChip managed={stack.managed} />
      </TableCell>
      <TableCell>
        <StackStatus stack={stack} />
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
