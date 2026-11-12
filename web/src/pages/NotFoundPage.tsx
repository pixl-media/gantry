import { Button, Stack, Typography } from '@mui/material';
import { useTranslation } from 'react-i18next';
import { Link as RouterLink } from 'react-router';

export function NotFoundPage() {
  const { t } = useTranslation();
  return (
    <Stack sx={{ gap: 2, alignItems: 'flex-start' }}>
      <Typography variant="h4" component="h1">
        {t('notFound.title')}
      </Typography>
      <Button component={RouterLink} to="/" variant="contained">
        {t('notFound.back')}
      </Button>
    </Stack>
  );
}
