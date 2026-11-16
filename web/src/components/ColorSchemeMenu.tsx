import CheckIcon from '@mui/icons-material/Check';
import DarkModeOutlinedIcon from '@mui/icons-material/DarkModeOutlined';
import LightModeOutlinedIcon from '@mui/icons-material/LightModeOutlined';
import SettingsBrightnessIcon from '@mui/icons-material/SettingsBrightness';
import {
  IconButton,
  ListItemIcon,
  ListItemText,
  Menu,
  MenuItem,
  Tooltip,
} from '@mui/material';
import { useColorScheme } from '@mui/material/styles';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';

const modes = [
  { mode: 'system', Icon: SettingsBrightnessIcon },
  { mode: 'light', Icon: LightModeOutlinedIcon },
  { mode: 'dark', Icon: DarkModeOutlinedIcon },
] as const;

/** Picks light, dark or the operating system's color scheme. */
export function ColorSchemeMenu() {
  const { t } = useTranslation();
  const { mode, setMode } = useColorScheme();
  const [anchor, setAnchor] = useState<HTMLElement | null>(null);
  const current = modes.find((m) => m.mode === mode) ?? modes[0];

  return (
    <>
      <Tooltip title={t('app.colorScheme')}>
        <IconButton
          color="inherit"
          aria-label={t('app.colorScheme')}
          aria-haspopup="menu"
          onClick={(e) => setAnchor(e.currentTarget)}
        >
          <current.Icon />
        </IconButton>
      </Tooltip>
      <Menu
        anchorEl={anchor}
        open={anchor !== null}
        onClose={() => setAnchor(null)}
      >
        {modes.map(({ mode: m, Icon }) => (
          <MenuItem
            key={m}
            selected={m === current.mode}
            onClick={() => {
              setMode(m);
              setAnchor(null);
            }}
          >
            <ListItemIcon>
              <Icon fontSize="small" />
            </ListItemIcon>
            <ListItemText>{t(`colorScheme.${m}`)}</ListItemText>
            {m === current.mode && (
              <CheckIcon fontSize="small" sx={{ ml: 2 }} />
            )}
          </MenuItem>
        ))}
      </Menu>
    </>
  );
}
