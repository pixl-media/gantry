import {
  Alert,
  Box,
  Button,
  Checkbox,
  Dialog,
  DialogActions,
  DialogContent,
  DialogContentText,
  DialogTitle,
  FormControlLabel,
  Stack,
  TextField,
  Typography,
} from '@mui/material';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router';
import type { StackDetail } from '../api/client';
import { useDeleteStack } from '../api/queries';
import { errorMessage } from '../lib/errors';
import { monoFont } from '../theme';
import { useNotify } from './Notifications';
import { useOperationPanel } from './OperationPanel';

interface Props {
  open: boolean;
  onClose: () => void;
  stack: Pick<StackDetail, 'name' | 'path' | 'volumes'>;
}

/**
 * Deletes a stack permanently. The button only works once the name has been
 * typed; volumes are only deleted if explicitly checked.
 */
export function DeleteStackDialog({ open, onClose, stack }: Props) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const notify = useNotify();
  const openPanel = useOperationPanel();
  const del = useDeleteStack();
  const [typed, setTyped] = useState('');
  const [volumes, setVolumes] = useState(false);

  const close = () => {
    setTyped('');
    setVolumes(false);
    onClose();
  };

  const submit = () =>
    del.mutate(
      {
        params: {
          path: { name: stack.name },
          query: { confirm: typed, volumes },
        },
      },
      {
        onSuccess: (op) => {
          close();
          openPanel(op);
          navigate('/');
        },
        onError: (e) => notify(errorMessage(t, e)),
      },
    );

  return (
    <Dialog open={open} onClose={close} maxWidth="sm" fullWidth>
      <DialogTitle>{t('delete.title', { name: stack.name })}</DialogTitle>
      <DialogContent>
        <Stack sx={{ gap: 2 }}>
          <Alert severity="error">{t('delete.text')}</Alert>
          <DialogContentText sx={{ fontFamily: monoFont, fontSize: 13 }}>
            {t('delete.path', { path: stack.path })}
          </DialogContentText>
          {stack.volumes.length === 0 ? (
            <Typography variant="body2" color="text.secondary">
              {t('delete.noVolumes')}
            </Typography>
          ) : (
            <Box>
              <Typography variant="body2" color={volumes ? 'error' : undefined}>
                {volumes ? t('delete.volumesGone') : t('delete.volumesHint')}
              </Typography>
              <Box
                component="ul"
                sx={{ my: 1, fontFamily: monoFont, fontSize: 13 }}
              >
                {stack.volumes.map((v) => (
                  <li key={v}>{v}</li>
                ))}
              </Box>
              <FormControlLabel
                control={
                  <Checkbox
                    color="error"
                    checked={volumes}
                    onChange={(e) => setVolumes(e.target.checked)}
                  />
                }
                label={t('delete.volumes')}
              />
            </Box>
          )}
          <TextField
            label={t('delete.confirmLabel', { name: stack.name })}
            value={typed}
            onChange={(e) => setTyped(e.target.value)}
            autoComplete="off"
            slotProps={{ htmlInput: { style: { fontFamily: monoFont } } }}
          />
        </Stack>
      </DialogContent>
      <DialogActions>
        <Button onClick={close}>{t('delete.cancel')}</Button>
        <Button
          variant="contained"
          color="error"
          disabled={typed !== stack.name || del.isPending}
          onClick={submit}
        >
          {t('delete.submit')}
        </Button>
      </DialogActions>
    </Dialog>
  );
}
