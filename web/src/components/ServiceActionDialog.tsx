import {
  Alert,
  Button,
  Dialog,
  DialogActions,
  DialogContent,
  DialogContentText,
  DialogTitle,
  List,
  ListItem,
  ListItemText,
} from '@mui/material';
import { useTranslation } from 'react-i18next';
import type { ServiceActionPlan } from '../api/client';

interface Props {
  /** The plan to confirm; the dialog is open while it is set. */
  plan: ServiceActionPlan | undefined;
  /** The agent sent a new plan after the previous one was confirmed. */
  changed: boolean;
  pending: boolean;
  onConfirm: (plan: ServiceActionPlan) => void;
  onClose: () => void;
}

/**
 * Asks before a service action that changes other services too, listing
 * every affected service with what happens to it and why.
 */
export function ServiceActionDialog({
  plan,
  changed,
  pending,
  onConfirm,
  onClose,
}: Props) {
  const { t } = useTranslation();
  const others = plan?.affected.filter((a) => a.reason !== 'target') ?? [];
  return (
    <Dialog open={plan !== undefined} onClose={onClose} maxWidth="xs" fullWidth>
      {plan && (
        <>
          <DialogTitle>
            {t(`service.confirmTitle.${plan.action}`, { name: plan.service })}
          </DialogTitle>
          <DialogContent>
            {changed && (
              <Alert severity="warning" sx={{ mb: 2 }}>
                {t('service.changed')}
              </Alert>
            )}
            <DialogContentText>{t('service.alsoAffected')}</DialogContentText>
            <List dense disablePadding>
              {others.map((a) => (
                <ListItem key={a.service} disableGutters>
                  <ListItemText
                    primary={`${a.service} – ${t(`service.effect.${a.effect}`)}`}
                    secondary={
                      a.reason === 'target'
                        ? undefined
                        : t(`service.reason.${a.reason}`, {
                            name: plan.service,
                          })
                    }
                  />
                </ListItem>
              ))}
            </List>
          </DialogContent>
          <DialogActions>
            <Button onClick={onClose}>{t('actions.cancel')}</Button>
            <Button
              variant="contained"
              color={plan.action === 'stop' ? 'error' : 'primary'}
              disabled={pending}
              onClick={() => onConfirm(plan)}
            >
              {t(`service.confirm.${plan.action}`, {
                n: plan.affected.length,
              })}
            </Button>
          </DialogActions>
        </>
      )}
    </Dialog>
  );
}
