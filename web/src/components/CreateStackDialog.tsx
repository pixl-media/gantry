import AddIcon from '@mui/icons-material/Add';
import RemoveCircleOutlineIcon from '@mui/icons-material/RemoveCircleOutlined';
import {
  Alert,
  AlertTitle,
  Box,
  Button,
  Checkbox,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  FormControlLabel,
  IconButton,
  Stack,
  TextField,
  Typography,
  useMediaQuery,
  useTheme,
} from '@mui/material';
import { lazy, Suspense, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router';
import { apiError } from '../api/client';
import { useCreateStack, useRunAction } from '../api/queries';
import { errorMessage } from '../lib/errors';
import { monoFont } from '../theme';
import { EditorFallback } from './ComposeTab';
import { useNotify } from './Notifications';
import { useOperationPanel } from './OperationPanel';

const CodeEditor = lazy(() => import('./CodeEditor'));

const namePattern = /^[a-z0-9][a-z0-9_-]*$/;
const keyPattern = /^[A-Za-z_][A-Za-z0-9_]*$/;

export const composeTemplate = `services:
  app:
    image: nginx:alpine
    restart: unless-stopped
    ports:
      - "8080:80"
`;

interface Props {
  open: boolean;
  onClose: () => void;
  /** Names already in use (managed and unmanaged stacks). */
  existing: string[];
}

export function CreateStackDialog({ open, onClose, existing }: Props) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const notify = useNotify();
  const openPanel = useOperationPanel();
  const create = useCreateStack();
  const run = useRunAction();
  const fullScreen = useMediaQuery(useTheme().breakpoints.down('sm'));

  const [name, setName] = useState('');
  const [compose, setCompose] = useState(composeTemplate);
  const [vars, setVars] = useState<{ key: string; value: string }[]>([]);
  const [start, setStart] = useState(true);
  const [invalidCompose, setInvalidCompose] = useState<string>();

  const nameError =
    name === ''
      ? undefined
      : !namePattern.test(name)
        ? t('create.nameHelp')
        : existing.includes(name)
          ? t('create.nameTaken')
          : undefined;
  const varError = (key: string, i: number) =>
    key === ''
      ? undefined
      : !keyPattern.test(key)
        ? t('env.invalidKey')
        : vars.findIndex((v) => v.key === key) !== i
          ? t('env.keyExists')
          : undefined;
  const valid =
    name !== '' &&
    nameError === undefined &&
    vars.every((v, i) => varError(v.key, i) === undefined);

  const reset = () => {
    setName('');
    setCompose(composeTemplate);
    setVars([]);
    setStart(true);
    setInvalidCompose(undefined);
  };

  const submit = () => {
    setInvalidCompose(undefined);
    const env = Object.fromEntries(
      vars.filter((v) => v.key !== '').map((v) => [v.key, v.value]),
    );
    create.mutate(
      { body: { name, compose, env } },
      {
        onSuccess: () => {
          const created = name;
          const done = () => {
            reset();
            onClose();
            navigate(`/stacks/${encodeURIComponent(created)}?tab=compose`);
          };
          if (!start) {
            done();
            return;
          }
          // Start before navigating away: callbacks passed to mutate don't
          // run once this dialog is unmounted.
          run.mutate(
            { params: { path: { name: created, action: 'start' } } },
            {
              onSuccess: (op) => openPanel(op),
              onError: (e) => notify(errorMessage(t, e)),
              onSettled: done,
            },
          );
        },
        onError: (e) => {
          const err = apiError(e);
          if (err?.code === 'invalid_compose') setInvalidCompose(err.error);
          else notify(errorMessage(t, e));
        },
      },
    );
  };

  return (
    <Dialog
      open={open}
      onClose={onClose}
      fullScreen={fullScreen}
      maxWidth="md"
      fullWidth
    >
      <DialogTitle>{t('create.title')}</DialogTitle>
      <DialogContent>
        <Stack sx={{ gap: 2, pt: 1 }}>
          <TextField
            label={t('create.name')}
            value={name}
            onChange={(e) => setName(e.target.value)}
            error={nameError !== undefined}
            helperText={nameError ?? t('create.nameHelp')}
            autoFocus
            required
            slotProps={{ htmlInput: { style: { fontFamily: monoFont } } }}
          />
          <Box>
            <Typography variant="subtitle2" gutterBottom>
              {t('create.compose')}
            </Typography>
            {invalidCompose && (
              <Alert severity="error" sx={{ mb: 1 }}>
                <AlertTitle>{t('compose.invalid')}</AlertTitle>
                <Box
                  component="pre"
                  sx={{ m: 0, fontFamily: monoFont, whiteSpace: 'pre-wrap' }}
                >
                  {invalidCompose}
                </Box>
              </Alert>
            )}
            <Suspense fallback={<EditorFallback />}>
              <CodeEditor
                value={compose}
                onChange={setCompose}
                ariaLabel={t('create.compose')}
                minHeight={240}
              />
            </Suspense>
          </Box>
          <Box>
            <Typography variant="subtitle2" gutterBottom>
              {t('create.variables')}
            </Typography>
            <Stack sx={{ gap: 1 }}>
              {vars.map((v, i) => (
                <Stack key={i} direction="row" sx={{ gap: 1 }}>
                  <TextField
                    size="small"
                    placeholder="NAME"
                    value={v.key}
                    error={varError(v.key, i) !== undefined}
                    helperText={varError(v.key, i)}
                    onChange={(e) =>
                      setVars(
                        vars.map((x, j) =>
                          j === i ? { ...x, key: e.target.value } : x,
                        ),
                      )
                    }
                    slotProps={{
                      htmlInput: {
                        'aria-label': t('env.name'),
                        style: { fontFamily: monoFont },
                      },
                    }}
                  />
                  <TextField
                    size="small"
                    sx={{ flex: 1 }}
                    value={v.value}
                    onChange={(e) =>
                      setVars(
                        vars.map((x, j) =>
                          j === i ? { ...x, value: e.target.value } : x,
                        ),
                      )
                    }
                    slotProps={{
                      htmlInput: {
                        'aria-label': t('env.value'),
                        style: { fontFamily: monoFont },
                      },
                    }}
                  />
                  <IconButton
                    aria-label={t('create.removeVariable')}
                    onClick={() => setVars(vars.filter((_, j) => j !== i))}
                  >
                    <RemoveCircleOutlineIcon />
                  </IconButton>
                </Stack>
              ))}
              <Button
                startIcon={<AddIcon />}
                sx={{ alignSelf: 'flex-start' }}
                onClick={() => setVars([...vars, { key: '', value: '' }])}
              >
                {t('create.addVariable')}
              </Button>
            </Stack>
          </Box>
          <FormControlLabel
            control={
              <Checkbox
                checked={start}
                onChange={(e) => setStart(e.target.checked)}
              />
            }
            label={t('create.start')}
          />
        </Stack>
      </DialogContent>
      <DialogActions>
        <Button onClick={onClose}>{t('create.cancel')}</Button>
        <Button
          variant="contained"
          disabled={!valid || create.isPending || run.isPending}
          onClick={submit}
        >
          {t('create.submit')}
        </Button>
      </DialogActions>
    </Dialog>
  );
}
