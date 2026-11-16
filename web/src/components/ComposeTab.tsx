import {
  Alert,
  AlertTitle,
  Box,
  Button,
  Chip,
  CircularProgress,
  Stack,
} from '@mui/material';
import { lazy, Suspense, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useBlocker } from 'react-router';
import { apiError, type StackDetail } from '../api/client';
import { useRunAction, useUpdateCompose } from '../api/queries';
import { errorMessage } from '../lib/errors';
import { monoFont } from '../theme';
import { ConfirmDialog } from './ConfirmDialog';
import { useNotify } from './Notifications';
import { useOperationPanel } from './OperationPanel';

const CodeEditor = lazy(() => import('./CodeEditor'));

export function EditorFallback() {
  return (
    <Box sx={{ display: 'grid', placeItems: 'center', minHeight: 200 }}>
      <CircularProgress />
    </Box>
  );
}

/** The Compose file of a stack: editable if managed, read-only otherwise. */
export function ComposeTab({ stack }: { stack: StackDetail }) {
  const { t } = useTranslation();
  const notify = useNotify();
  const openPanel = useOperationPanel();
  const update = useUpdateCompose();
  const run = useRunAction();
  const file = stack.composeFile;
  const saved = file?.content;

  // The draft, with the version it is based on; null while not editing, so
  // the editor follows the stored file (reloaded whenever the stack changes).
  const [draft, setDraft] = useState<{
    content: string;
    version: string;
  } | null>(null);
  const [problem, setProblem] = useState<'conflict' | { invalid: string }>();
  const dirty = draft !== null && draft.content !== saved;

  // Ask before leaving with unsaved changes: in the app and when closing.
  const blocker = useBlocker(dirty);
  useEffect(() => {
    if (!dirty) return;
    const warn = (e: BeforeUnloadEvent) => e.preventDefault();
    window.addEventListener('beforeunload', warn);
    return () => window.removeEventListener('beforeunload', warn);
  }, [dirty]);

  if (!file || saved === undefined) {
    return <Alert severity="info">{t('stack.composeUnreadable')}</Alert>;
  }

  const save = (apply: boolean) => {
    if (!draft) return;
    setProblem(undefined);
    update.mutate(
      {
        params: { path: { name: stack.name } },
        body: { content: draft.content, version: draft.version },
      },
      {
        onSuccess: () => {
          setDraft(null);
          notify(t('compose.saved'), 'success');
          if (apply) {
            run.mutate(
              { params: { path: { name: stack.name, action: 'start' } } },
              {
                onSuccess: (op) => openPanel(op),
                onError: (e) => notify(errorMessage(t, e)),
              },
            );
          }
        },
        onError: (e) => {
          const err = apiError(e);
          if (err?.code === 'version_conflict') setProblem('conflict');
          else if (err?.code === 'invalid_compose')
            setProblem({ invalid: err.error });
          else notify(errorMessage(t, e));
        },
      },
    );
  };

  return (
    <Stack sx={{ gap: 1.5 }}>
      {!stack.managed && <Alert severity="info">{t('compose.readOnly')}</Alert>}
      {problem === 'conflict' && (
        <Alert
          severity="warning"
          action={
            <Button
              color="inherit"
              size="small"
              onClick={() => {
                setDraft(null);
                setProblem(undefined);
              }}
            >
              {t('compose.reload')}
            </Button>
          }
        >
          {t('compose.conflict')}
        </Alert>
      )}
      {problem && problem !== 'conflict' && (
        <Alert severity="error">
          <AlertTitle>{t('compose.invalid')}</AlertTitle>
          <Box
            component="pre"
            sx={{ m: 0, fontFamily: monoFont, whiteSpace: 'pre-wrap' }}
          >
            {problem.invalid}
          </Box>
        </Alert>
      )}

      <Suspense fallback={<EditorFallback />}>
        <CodeEditor
          value={draft?.content ?? saved}
          readOnly={!stack.managed}
          ariaLabel={t('compose.editorLabel')}
          onChange={(content) =>
            setDraft((d) => ({
              content,
              version: d?.version ?? file.version ?? '',
            }))
          }
        />
      </Suspense>

      {stack.managed && (
        <Stack direction="row" sx={{ gap: 1, alignItems: 'center' }}>
          <Button
            variant="contained"
            disabled={!dirty || update.isPending}
            onClick={() => save(false)}
          >
            {t('compose.save')}
          </Button>
          <Button
            variant="outlined"
            disabled={!dirty || update.isPending}
            onClick={() => save(true)}
          >
            {t('compose.saveAndApply')}
          </Button>
          <Button
            disabled={!dirty || update.isPending}
            onClick={() => {
              setDraft(null);
              setProblem(undefined);
            }}
          >
            {t('compose.discard')}
          </Button>
          {dirty && (
            <Chip size="small" color="warning" label={t('compose.unsaved')} />
          )}
        </Stack>
      )}

      <ConfirmDialog
        open={blocker.state === 'blocked'}
        title={t('compose.leaveTitle')}
        text={t('compose.leaveText')}
        confirmLabel={t('compose.leave')}
        cancelLabel={t('compose.stay')}
        onConfirm={() => blocker.proceed?.()}
        onClose={() => blocker.state === 'blocked' && blocker.reset()}
      />
    </Stack>
  );
}
