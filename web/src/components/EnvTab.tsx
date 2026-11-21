import AddIcon from '@mui/icons-material/Add';
import CheckIcon from '@mui/icons-material/Check';
import CloseIcon from '@mui/icons-material/Close';
import DeleteIcon from '@mui/icons-material/Delete';
import EditIcon from '@mui/icons-material/Edit';
import VisibilityIcon from '@mui/icons-material/Visibility';
import VisibilityOffIcon from '@mui/icons-material/VisibilityOff';
import {
  Alert,
  Box,
  Button,
  CircularProgress,
  IconButton,
  Paper,
  Stack,
  Table,
  TableBody,
  TableCell,
  TableContainer,
  TableHead,
  TableRow,
  TextField,
  Tooltip,
  Typography,
} from '@mui/material';
import { useState, type KeyboardEvent } from 'react';
import { useTranslation } from 'react-i18next';
import {
  useEnv,
  useRevealEnv,
  useRunAction,
  useUpdateEnv,
} from '../api/queries';
import { errorMessage } from '../lib/errors';
import { monoFont } from '../theme';
import { ConfirmDialog } from './ConfirmDialog';
import { useNotify } from './Notifications';
import { useOperationPanel } from './OperationPanel';

const keyPattern = /^[A-Za-z_][A-Za-z0-9_]*$/;

/** A copy of values without key. */
function without(values: Record<string, string>, key: string) {
  const rest = { ...values };
  delete rest[key];
  return rest;
}

/**
 * The variables of a stack's .env file. Only names are loaded; a value is
 * fetched on purpose (eye or edit) and kept in this component only, so it is
 * gone when the tab is left.
 */
export function EnvTab({
  stack,
  editable,
}: {
  stack: string;
  /** False if the agent may not change the file; values can still be shown. */
  editable: boolean;
}) {
  const { t } = useTranslation();
  const notify = useNotify();
  const openPanel = useOperationPanel();
  const env = useEnv(stack);
  const update = useUpdateEnv(stack);
  const reveal = useRevealEnv();
  const run = useRunAction();

  const [revealed, setRevealed] = useState<Record<string, string>>({});
  const [editing, setEditing] = useState<{ key: string; value: string }>();
  const [adding, setAdding] = useState<{ key: string; value: string }>();
  const [deleting, setDeleting] = useState<string>();
  const [changed, setChanged] = useState(false);

  if (env.isPending) return <CircularProgress />;
  if (env.isError) {
    return <Alert severity="error">{errorMessage(t, env.error)}</Alert>;
  }
  const { variables, version } = env.data;
  const keys = variables.map((v) => v.key);

  const fetchValue = (key: string, then?: (value: string) => void) =>
    reveal.mutate(
      { params: { path: { name: stack } }, body: { key } },
      {
        onSuccess: (d) => {
          setRevealed((r) => ({ ...r, [key]: d.value }));
          then?.(d.value);
        },
        onError: (e) => notify(errorMessage(t, e)),
      },
    );

  const change = (
    body: { set?: Record<string, string>; delete?: string[] },
    done: () => void,
  ) =>
    update.mutate(
      { params: { path: { name: stack } }, body: { version, ...body } },
      {
        onSuccess: () => {
          done();
          setChanged(true);
        },
        onError: (e) => {
          notify(errorMessage(t, e));
          void env.refetch();
        },
      },
    );

  const addKeyError =
    adding && adding.key !== ''
      ? !keyPattern.test(adding.key)
        ? t('env.invalidKey')
        : keys.includes(adding.key)
          ? t('env.keyExists')
          : undefined
      : undefined;

  const saveEdit = () => {
    if (!editing || update.isPending) return;
    const { key, value } = editing;
    change({ set: { [key]: value } }, () => {
      setRevealed((r) => ({ ...r, [key]: value }));
      setEditing(undefined);
    });
  };
  const canAdd =
    adding !== undefined &&
    adding.key !== '' &&
    addKeyError === undefined &&
    !update.isPending;
  const saveAdd = () => {
    if (!adding || !canAdd) return;
    const { key, value } = adding;
    change({ set: { [key]: value } }, () => {
      setRevealed((r) => ({ ...r, [key]: value }));
      setAdding(undefined);
    });
  };
  /** Enter saves, Escape cancels, as in a form. */
  const rowKeys =
    (save: () => void, cancel: () => void) => (e: KeyboardEvent) => {
      if (e.key === 'Enter') {
        e.preventDefault();
        save();
      } else if (e.key === 'Escape') {
        e.preventDefault();
        cancel();
      }
    };

  return (
    <Stack sx={{ gap: 1.5 }}>
      <Alert severity="info">{t('env.hint')}</Alert>
      {changed && (
        <Alert
          severity="warning"
          action={
            <Button
              color="inherit"
              size="small"
              disabled={run.isPending}
              onClick={() =>
                run.mutate(
                  { params: { path: { name: stack, action: 'start' } } },
                  {
                    onSuccess: (op) => {
                      setChanged(false);
                      openPanel(op);
                    },
                    onError: (e) => notify(errorMessage(t, e)),
                  },
                )
              }
            >
              {t('env.applyNow')}
            </Button>
          }
        >
          {t('env.applyHint')}
        </Alert>
      )}

      <TableContainer component={Paper} variant="outlined">
        <Table size="small">
          <TableHead>
            <TableRow>
              <TableCell>{t('env.name')}</TableCell>
              <TableCell>{t('env.value')}</TableCell>
              <TableCell />
            </TableRow>
          </TableHead>
          <TableBody>
            {keys.length === 0 && (
              <TableRow>
                <TableCell colSpan={3} sx={{ color: 'text.secondary' }}>
                  {t('env.empty')}
                </TableCell>
              </TableRow>
            )}
            {keys.map((key) => {
              const shown = key in revealed;
              const isEditing = editing?.key === key;
              return (
                <TableRow key={key}>
                  <TableCell sx={{ fontFamily: monoFont, fontWeight: 600 }}>
                    {key}
                  </TableCell>
                  <TableCell sx={{ fontFamily: monoFont, width: '55%' }}>
                    {isEditing ? (
                      <TextField
                        size="small"
                        fullWidth
                        autoFocus
                        value={editing.value}
                        onChange={(e) =>
                          setEditing({ key, value: e.target.value })
                        }
                        onKeyDown={rowKeys(saveEdit, () =>
                          setEditing(undefined),
                        )}
                        slotProps={{
                          htmlInput: {
                            'aria-label': t('env.value'),
                            style: { fontFamily: monoFont },
                          },
                        }}
                      />
                    ) : shown ? (
                      <Box
                        component="span"
                        data-testid={`value-${key}`}
                        sx={{ wordBreak: 'break-all' }}
                      >
                        {revealed[key]}
                      </Box>
                    ) : (
                      <Typography component="span" color="textSecondary">
                        ••••••
                      </Typography>
                    )}
                  </TableCell>
                  <TableCell align="right" sx={{ whiteSpace: 'nowrap' }}>
                    {isEditing ? (
                      <>
                        <Tooltip title={t('env.save')}>
                          <IconButton
                            size="small"
                            aria-label={t('env.save')}
                            disabled={update.isPending}
                            onClick={saveEdit}
                          >
                            <CheckIcon fontSize="small" />
                          </IconButton>
                        </Tooltip>
                        <Tooltip title={t('env.cancel')}>
                          <IconButton
                            size="small"
                            aria-label={t('env.cancel')}
                            onClick={() => setEditing(undefined)}
                          >
                            <CloseIcon fontSize="small" />
                          </IconButton>
                        </Tooltip>
                      </>
                    ) : (
                      <>
                        <Tooltip
                          title={shown ? t('env.hide') : t('env.reveal')}
                        >
                          <IconButton
                            size="small"
                            aria-label={`${shown ? t('env.hide') : t('env.reveal')}: ${key}`}
                            disabled={reveal.isPending}
                            onClick={() =>
                              shown
                                ? setRevealed((r) => without(r, key))
                                : fetchValue(key)
                            }
                          >
                            {shown ? (
                              <VisibilityOffIcon fontSize="small" />
                            ) : (
                              <VisibilityIcon fontSize="small" />
                            )}
                          </IconButton>
                        </Tooltip>
                        {editable && (
                          <>
                            <Tooltip title={t('env.edit')}>
                              <IconButton
                                size="small"
                                aria-label={`${t('env.edit')}: ${key}`}
                                onClick={() =>
                                  shown
                                    ? setEditing({ key, value: revealed[key]! })
                                    : fetchValue(key, (value) =>
                                        setEditing({ key, value }),
                                      )
                                }
                              >
                                <EditIcon fontSize="small" />
                              </IconButton>
                            </Tooltip>
                            <Tooltip title={t('env.delete')}>
                              <IconButton
                                size="small"
                                aria-label={`${t('env.delete')}: ${key}`}
                                onClick={() => setDeleting(key)}
                              >
                                <DeleteIcon fontSize="small" />
                              </IconButton>
                            </Tooltip>
                          </>
                        )}
                      </>
                    )}
                  </TableCell>
                </TableRow>
              );
            })}
            {adding && (
              <TableRow>
                <TableCell>
                  <TextField
                    size="small"
                    autoFocus
                    placeholder="NAME"
                    value={adding.key}
                    error={addKeyError !== undefined}
                    helperText={addKeyError}
                    onChange={(e) =>
                      setAdding({ ...adding, key: e.target.value })
                    }
                    onKeyDown={rowKeys(saveAdd, () => setAdding(undefined))}
                    slotProps={{
                      htmlInput: {
                        'aria-label': t('env.name'),
                        style: { fontFamily: monoFont },
                      },
                    }}
                  />
                </TableCell>
                <TableCell>
                  <TextField
                    size="small"
                    fullWidth
                    value={adding.value}
                    onChange={(e) =>
                      setAdding({ ...adding, value: e.target.value })
                    }
                    onKeyDown={rowKeys(saveAdd, () => setAdding(undefined))}
                    slotProps={{
                      htmlInput: {
                        'aria-label': t('env.value'),
                        style: { fontFamily: monoFont },
                      },
                    }}
                  />
                </TableCell>
                <TableCell align="right" sx={{ whiteSpace: 'nowrap' }}>
                  <IconButton
                    size="small"
                    aria-label={t('env.save')}
                    disabled={!canAdd}
                    onClick={saveAdd}
                  >
                    <CheckIcon fontSize="small" />
                  </IconButton>
                  <IconButton
                    size="small"
                    aria-label={t('env.cancel')}
                    onClick={() => setAdding(undefined)}
                  >
                    <CloseIcon fontSize="small" />
                  </IconButton>
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </TableContainer>

      {editable && !adding && (
        <Button
          startIcon={<AddIcon />}
          sx={{ alignSelf: 'flex-start' }}
          onClick={() => setAdding({ key: '', value: '' })}
        >
          {t('env.add')}
        </Button>
      )}

      <ConfirmDialog
        open={deleting !== undefined}
        title={t('env.confirmDeleteTitle', { key: deleting })}
        text={t('env.confirmDeleteText')}
        confirmLabel={t('env.delete')}
        onConfirm={() => {
          const key = deleting!;
          change({ delete: [key] }, () => setRevealed((r) => without(r, key)));
        }}
        onClose={() => setDeleting(undefined)}
      />
    </Stack>
  );
}
