import {
  Alert,
  Box,
  Button,
  Card,
  CardContent,
  Stack,
  TextField,
  Typography,
} from '@mui/material';
import { useState, type FormEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { Navigate, useLocation, useNavigate } from 'react-router';
import { apiError } from '../api/client';
import { useLogin, useSession } from '../api/queries';
import { Logo } from '../components/Logo';

export function LoginPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const location = useLocation();
  const session = useSession();
  const login = useLogin();
  const [token, setToken] = useState('');

  const from = (location.state as { from?: string } | null)?.from ?? '/';
  if (session.isSuccess) return <Navigate to={from} replace />;

  const submit = (e: FormEvent) => {
    e.preventDefault();
    login.mutate(
      { body: { token } },
      { onSuccess: () => navigate(from, { replace: true }) },
    );
  };

  let error: string | undefined;
  if (login.isError) {
    const e = apiError(login.error);
    error =
      e?.code === 'invalid_token'
        ? t('login.invalidToken')
        : e?.code === 'too_many_attempts'
          ? t('login.tooManyAttempts')
          : e?.code === 'login_disabled'
            ? t('login.disabled')
            : t('login.failed', { message: e?.error ?? String(login.error) });
  }

  return (
    <Box
      sx={{
        minHeight: '100vh',
        display: 'grid',
        placeItems: 'center',
        bgcolor: 'background.default',
        p: 2,
      }}
    >
      <Card sx={{ width: '100%', maxWidth: 420 }} variant="outlined">
        <CardContent>
          <Stack component="form" onSubmit={submit} sx={{ gap: 2 }}>
            <Stack direction="row" sx={{ alignItems: 'center', gap: 1.5 }}>
              <Logo height={36} />
              <Typography variant="h5" component="h1" sx={{ fontWeight: 700 }}>
                {t('app.title')}
              </Typography>
            </Stack>
            <Typography variant="h6" component="h2">
              {t('login.title')}
            </Typography>
            <Typography variant="body2" color="text.secondary">
              {t('login.subtitle')}
            </Typography>
            {error && <Alert severity="error">{error}</Alert>}
            <TextField
              label={t('login.token')}
              type="password"
              autoComplete="current-password"
              autoFocus
              required
              value={token}
              onChange={(e) => setToken(e.target.value)}
            />
            <Button
              type="submit"
              variant="contained"
              size="large"
              disabled={login.isPending || token === ''}
            >
              {t('login.submit')}
            </Button>
          </Stack>
        </CardContent>
      </Card>
    </Box>
  );
}
