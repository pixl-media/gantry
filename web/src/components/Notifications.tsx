import { Alert, Snackbar, type AlertColor } from '@mui/material';
import {
  createContext,
  useCallback,
  useContext,
  useState,
  type ReactNode,
} from 'react';

type Notify = (message: string, severity?: AlertColor) => void;

const NotifyContext = createContext<Notify>(() => {});

/** Shows short messages, e.g. failed requests, at the bottom of the page. */
export function NotificationProvider({ children }: { children: ReactNode }) {
  const [current, setCurrent] = useState<{
    message: string;
    severity: AlertColor;
    key: number;
  }>();

  const notify = useCallback<Notify>((message, severity = 'error') => {
    setCurrent({ message, severity, key: Date.now() });
  }, []);

  return (
    <NotifyContext.Provider value={notify}>
      {children}
      <Snackbar
        key={current?.key}
        open={current !== undefined}
        autoHideDuration={6000}
        onClose={() => setCurrent(undefined)}
        anchorOrigin={{ vertical: 'bottom', horizontal: 'center' }}
      >
        <Alert
          severity={current?.severity}
          variant="filled"
          onClose={() => setCurrent(undefined)}
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
