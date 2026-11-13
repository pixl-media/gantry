import { createRoutesFromElements, Route } from 'react-router';
import { AppLayout } from './components/AppLayout';
import { RequireAuth } from './components/RequireAuth';
import { LoginPage } from './pages/LoginPage';
import { NotFoundPage } from './pages/NotFoundPage';
import { StackPage } from './pages/StackPage';
import { StacksPage } from './pages/StacksPage';

/**
 * The app's routes, for a data router (createBrowserRouter). A data router
 * is needed for useBlocker, which asks before leaving unsaved edits.
 */
export const routes = createRoutesFromElements(
  <>
    <Route path="/login" element={<LoginPage />} />
    <Route
      element={
        <RequireAuth>
          <AppLayout />
        </RequireAuth>
      }
    >
      <Route index element={<StacksPage />} />
      <Route path="stacks/:name" element={<StackPage />} />
      <Route path="*" element={<NotFoundPage />} />
    </Route>
  </>,
);
