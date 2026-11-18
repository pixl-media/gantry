import { Alert, Button, Snackbar, type AlertColor } from '@mui/material';
import {
  createContext,
  useCallback,
  useContext,
  useState,
  type ReactNode,
} from 'react';

/** A button in a message, such as "Show output". */
export type NotifyAction = { label: string; onClick: () => void };

type Notify = (
  message: string,
  severity?: AlertColor,
  action?: NotifyAction,
) => void;

const NotifyContext = createContext<Notify>(() => {});

/**
 * Shows short messages, e.g. failed requests or finished operations, at the
 * top right, below the app bar so that its buttons stay usable.
 */
export function NotificationProvider({ children }: { children: ReactNode }) {
  const [current, setCurrent] = useState<{
    message: string;
    severity: AlertColor;
    action?: NotifyAction;
    key: number;
  }>();

  const notify = useCallback<Notify>((message, severity = 'error', action) => {
    setCurrent({ message, severity, action, key: Date.now() });
  }, []);
  const close = () => setCurrent(undefined);

  return (
    <NotifyContext.Provider value={notify}>
      {children}
      <Snackbar
        key={current?.key}
        open={current !== undefined}
        autoHideDuration={current?.action ? 10_000 : 6000}
        onClose={close}
        anchorOrigin={{ vertical: 'top', horizontal: 'right' }}
        // Below the app bar (56 px on phones, 64 px from sm).
        sx={{ top: { xs: 64, sm: 72 } }}
      >
        <Alert
          severity={current?.severity}
          variant="filled"
          onClose={close}
          action={
            current?.action && (
              <Button
                color="inherit"
                size="small"
                onClick={() => {
                  current.action?.onClick();
                  close();
                }}
              >
                {current.action.label}
              </Button>
            )
          }
        >
          {current?.message}
        </Alert>
      </Snackbar>
    </NotifyContext.Provider>
  );
}

export function useNotify(): Notify {
  return useContext(NotifyContext);
}
