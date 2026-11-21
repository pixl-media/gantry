import { Typography } from '@mui/material';
import { useTranslation } from 'react-i18next';
import { useAgentVersion } from '../api/queries';
import { differentReleases, webVersion } from '../lib/version';

/**
 * The versions of the web UI and the agent, at the bottom of every page, so
 * it's clear what runs and whether both are from the same release.
 */
export function Versions() {
  const { t } = useTranslation();
  const agent = useAgentVersion().data?.version;
  const mismatch = agent !== undefined && differentReleases(webVersion, agent);

  return (
    <Typography
      component="p"
      variant="caption"
      color={mismatch ? 'warning' : 'textSecondary'}
      sx={{ textAlign: 'center', pb: 2 }}
    >
      {t('app.versions', { web: webVersion, agent: agent ?? '…' })}
      {mismatch && ` · ${t('app.versionMismatch')}`}
    </Typography>
  );
}
