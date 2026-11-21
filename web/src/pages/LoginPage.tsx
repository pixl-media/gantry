import {
  Alert,
  Box,
  Button,
  Card,
  CardContent,
  Divider,
  Stack,
  TextField,
  Typography,
} from '@mui/material';
import { useEffect, useState, type FormEvent } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Navigate,
  useLocation,
  useNavigate,
  useSearchParams,
} from 'react-router';
import { apiError } from '../api/client';
import { useLogin, useLoginMethods, useSession } from '../api/queries';
import { Logo } from '../components/Logo';

export function LoginPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const location = useLocation();
  const session = useSession();
  const methods = useLoginMethods();
  const login = useLogin();
  const [token, setToken] = useState('');
  // Single sign-on comes back here with ?error=<code> if it failed; the
  // message stays, the parameter goes, so a reload doesn't repeat it.
  const [params, setParams] = useSearchParams();
  const [ssoError] = useState(() => params.get('error'));
  useEffect(() => {
    if (params.has('error')) setParams({}, { replace: true });
  }, [params, setParams]);

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
            ? t('login.tokenDisabled')
            : t('login.failed', { message: e?.error ?? String(login.error) });
  } else if (ssoError) {
    error = ssoErrorText(ssoError, t);
  }

  // If the agent can't say, the token form is shown, as before.
  const sso = methods.data?.oidc ?? false;
  const tokenLogin = methods.data?.token ?? methods.isError;
  const off = methods.isSuccess && !sso && !tokenLogin;

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
          <Stack sx={{ gap: 2 }}>
            <Stack direction="row" sx={{ alignItems: 'center', gap: 1.5 }}>
              <Logo height={36} />
              <Typography variant="h5" component="h1" sx={{ fontWeight: 700 }}>
                {t('app.title')}
              </Typography>
            </Stack>
            <Typography variant="h6" component="h2">
              {t('login.title')}
            </Typography>
            {error && <Alert severity="error">{error}</Alert>}
            {off && <Alert severity="info">{t('login.disabled')}</Alert>}
            {sso && (
              <Button
                variant="contained"
                size="large"
                href={`/api/v1/session/oidc?returnTo=${encodeURIComponent(from)}`}
              >
                {t('login.sso')}
              </Button>
            )}
            {sso && tokenLogin && (
              <Divider>
                <Typography variant="body2" color="textSecondary">
                  {t('login.or')}
                </Typography>
              </Divider>
            )}
            {tokenLogin && (
              <Stack component="form" onSubmit={submit} sx={{ gap: 2 }}>
                <Typography variant="body2" color="textSecondary">
                  {t('login.subtitle')}
                </Typography>
                <TextField
                  label={t('login.token')}
                  type="password"
                  autoComplete="current-password"
                  autoFocus={!sso}
                  required
                  value={token}
                  onChange={(e) => setToken(e.target.value)}
                />
                <Button
                  type="submit"
                  variant={sso ? 'outlined' : 'contained'}
                  size="large"
                  disabled={login.isPending || token === ''}
                >
                  {t('login.submit')}
                </Button>
              </Stack>
            )}
          </Stack>
        </CardContent>
      </Card>
    </Box>
  );
}

/** The message for an error code single sign-on came back with. */
function ssoErrorText(
  code: string,
  t: ReturnType<typeof useTranslation>['t'],
): string {
  switch (code) {
    case 'oidc_denied':
      return t('login.ssoDenied');
    case 'oidc_unavailable':
      return t('login.ssoUnavailable');
    case 'oidc_disabled':
      return t('login.ssoDisabled');
    case 'https_required':
      return t('login.httpsRequired');
    case 'too_many_attempts':
      return t('login.tooManyAttempts');
    default:
      return t('login.ssoFailed');
  }
}
