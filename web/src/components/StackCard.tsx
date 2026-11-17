import {
  Box,
  Card,
  CardActions,
  CardContent,
  Link,
  Stack,
  Typography,
} from '@mui/material';
import { useTranslation } from 'react-i18next';
import { Link as RouterLink } from 'react-router';
import type { StackSummary } from '../api/client';
import { ActionButtons } from './ActionButtons';
import { StackStatus, TypeChip } from './StackBadges';

// Above the link's overlay, so buttons and tooltips work there.
const raised = { position: 'relative', zIndex: 1 } as const;

/**
 * A stack as a card, for small screens and as an alternative to the table.
 * The whole card opens the stack: the name's link covers it ("stretched
 * link"), so there is one link and no buttons inside it.
 */
export function StackCard({
  stack,
  listSearch,
}: {
  stack: StackSummary;
  /** The list's filter, for the way back from the stack. */
  listSearch: string;
}) {
  const { t } = useTranslation();
  return (
    <Card
      variant="outlined"
      sx={{
        position: 'relative',
        display: 'flex',
        flexDirection: 'column',
        '&:hover': { borderColor: 'primary.main' },
      }}
    >
      <CardContent
        sx={{ flex: 1, display: 'flex', flexDirection: 'column', gap: 1 }}
      >
        <Stack direction="row" sx={{ alignItems: 'center', gap: 1 }}>
          <Link
            component={RouterLink}
            to={`/stacks/${encodeURIComponent(stack.name)}`}
            state={{ listSearch }}
            noWrap
            sx={{
              display: 'block',
              flex: 1,
              minWidth: 0,
              fontWeight: 600,
              '&::after': { content: '""', position: 'absolute', inset: 0 },
            }}
          >
            {stack.name}
          </Link>
          <Box sx={raised}>
            <TypeChip managed={stack.managed} />
          </Box>
        </Stack>
        <Typography
          variant="caption"
          color="text.secondary"
          sx={{ overflowWrap: 'anywhere' }}
        >
          {stack.path}
        </Typography>
        <Stack direction="row" sx={{ alignItems: 'center', gap: 1.5 }}>
          <Box sx={raised}>
            <StackStatus stack={stack} />
          </Box>
          <Typography variant="body2" color="text.secondary">
            {t('stacks.servicesCount', {
              running: stack.servicesRunning,
              total: stack.servicesTotal,
            })}
          </Typography>
        </Stack>
      </CardContent>
      {stack.managed && (
        <CardActions sx={{ ...raised, pt: 0 }}>
          <ActionButtons stack={stack} />
        </CardActions>
      )}
    </Card>
  );
}
