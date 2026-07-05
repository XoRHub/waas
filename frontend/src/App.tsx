import { BrowserRouter, Route, Routes } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ProtectedRoute } from '@/components/ProtectedRoute';
import { LoginPage } from '@/pages/LoginPage';
import { PortalPage } from '@/pages/PortalPage';
import { ConnectPage } from '@/pages/ConnectPage';
import { ProfilePage } from '@/pages/ProfilePage';
import { AdminLayout } from '@/pages/admin/AdminLayout';
import { FleetPage } from '@/pages/admin/FleetPage';
import { TemplatesPage } from '@/pages/admin/TemplatesPage';
import { UsersPage } from '@/pages/admin/UsersPage';
import { GovernancePage } from '@/pages/admin/GovernancePage';
import { AuditPage } from '@/pages/admin/AuditPage';

const queryClient = new QueryClient({
  defaultOptions: {
    queries: { retry: 1, refetchOnWindowFocus: false },
  },
});

export function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <Routes>
          <Route path="/login" element={<LoginPage />} />
          <Route element={<ProtectedRoute />}>
            <Route path="/" element={<PortalPage />} />
            <Route path="/profile" element={<ProfilePage />} />
            <Route path="/workspaces/:id/connect" element={<ConnectPage />} />
          </Route>
          <Route element={<ProtectedRoute adminOnly />}>
            <Route path="/admin" element={<AdminLayout />}>
              <Route index element={<FleetPage />} />
              <Route path="templates" element={<TemplatesPage />} />
              <Route path="users" element={<UsersPage />} />
              <Route path="governance" element={<GovernancePage />} />
              <Route path="audit" element={<AuditPage />} />
            </Route>
          </Route>
        </Routes>
      </BrowserRouter>
    </QueryClientProvider>
  );
}
