import { Navigate, Outlet } from 'react-router';
import { useAuthStore } from '@/stores/authStore';

export function ProtectedRoute({ adminOnly = false }: { adminOnly?: boolean }) {
  const accessToken = useAuthStore((s) => s.accessToken);
  const user = useAuthStore((s) => s.user);

  if (!accessToken || !user) {
    return <Navigate to="/login" replace />;
  }
  if (adminOnly && user.role !== 'admin') {
    return <Navigate to="/" replace />;
  }
  return <Outlet />;
}
