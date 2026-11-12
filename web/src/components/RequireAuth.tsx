import { Box, CircularProgress } from '@mui/material';
import { useQueryClient } from '@tanstack/react-query';
import { useEffect, type ReactNode } from 'react';
import { Navigate, useLocation } from 'react-router';
import { setUnauthorizedHandler } from '../api/client';
import { useSession } from '../api/queries';

/** Shows children only when logged in; otherwise redirects to /login. */
export function RequireAuth({ children }: { children: ReactNode }) {
  const session = useSession();
  const location = useLocation();
  const queryClient = useQueryClient();

  // Any 401 (e.g. an expired session) re-checks the session, which then
  // redirects to the login page.
  useEffect(() => {
    setUnauthorizedHandler(() =>
      queryClient.invalidateQueries({ queryKey: ['get', '/api/v1/session'] }),
    );
  }, [queryClient]);

  if (session.isPending) {
    return (
      <Box sx={{ display: 'grid', placeItems: 'center', minHeight: '50vh' }}>
        <CircularProgress />
      </Box>
    );
  }
  if (session.isError) {
    return <Navigate to="/login" replace state={{ from: location.pathname }} />;
  }
  return children;
}
