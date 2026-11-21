import LogoutIcon from '@mui/icons-material/Logout';
import {
  AppBar,
  Box,
  Container,
  IconButton,
  Link,
  MenuItem,
  Select,
  Toolbar,
  Tooltip,
  Typography,
} from '@mui/material';
import { useTranslation } from 'react-i18next';
import { Outlet, Link as RouterLink, useNavigate } from 'react-router';
import { LiveProvider } from '../api/live';
import { useLogout, useSession } from '../api/queries';
import { languages } from '../i18n';
import { ColorSchemeMenu } from './ColorSchemeMenu';
import { Logo } from './Logo';
import { OperationWatcher } from './OperationWatcher';
import { Versions } from './Versions';

const languageNames: Record<(typeof languages)[number], string> = {
  en: 'English',
  de: 'Deutsch',
};

export function AppLayout() {
  const { t, i18n } = useTranslation();
  const navigate = useNavigate();
  const logout = useLogout();
  // Who logged in, for sessions from single sign-on.
  const user = useSession().data?.user;
  const current = languages.find((l) => i18n.resolvedLanguage === l) ?? 'en';

  return (
    <Box sx={{ minHeight: '100vh', bgcolor: 'background.default' }}>
      <AppBar position="sticky" elevation={0}>
        <Toolbar sx={{ gap: 2 }}>
          <Box sx={{ flex: 1 }}>
            <Link
              component={RouterLink}
              to="/"
              color="inherit"
              underline="none"
              sx={{ display: 'inline-flex', alignItems: 'center', gap: 1.25 }}
            >
              <Logo />
              <Typography
                variant="h6"
                component="span"
                sx={{ fontWeight: 700 }}
              >
                {t('app.title')}
              </Typography>
            </Link>
          </Box>
          <Select
            size="small"
            value={current}
            onChange={(e) => i18n.changeLanguage(e.target.value)}
            aria-label={t('app.language')}
            sx={{
              color: 'inherit',
              '.MuiOutlinedInput-notchedOutline': {
                borderColor: 'rgba(255,255,255,0.4)',
              },
              '.MuiSvgIcon-root': { color: 'inherit' },
            }}
          >
            {languages.map((l) => (
              <MenuItem key={l} value={l}>
                {languageNames[l]}
              </MenuItem>
            ))}
          </Select>
          <ColorSchemeMenu />
          <Tooltip
            title={user ? t('app.logoutUser', { user }) : t('app.logout')}
          >
            <IconButton
              color="inherit"
              aria-label={t('app.logout')}
              onClick={() =>
                logout.mutate({}, { onSettled: () => navigate('/login') })
              }
            >
              <LogoutIcon />
            </IconButton>
          </Tooltip>
        </Toolbar>
      </AppBar>
      <Container maxWidth="lg" sx={{ py: 3 }}>
        <LiveProvider>
          <Outlet />
          <OperationWatcher />
        </LiveProvider>
      </Container>
      <Versions />
    </Box>
  );
}
