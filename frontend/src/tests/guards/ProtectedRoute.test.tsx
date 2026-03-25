import { render, screen } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { MemoryRouter } from 'react-router-dom';
import { ProtectedRoute } from '../../shared/guards/ProtectedRoute';

const mockUseUserStore = vi.fn();
vi.mock('../../shared/store/user', () => ({
  useUserStore: (...args: unknown[]) => mockUseUserStore(...args),
}));

function renderWithRouter(ui: React.ReactElement, initialEntries = ['/']) {
  return render(<MemoryRouter initialEntries={initialEntries}>{ui}</MemoryRouter>);
}

describe('ProtectedRoute', () => {
  beforeEach(() => vi.clearAllMocks());

  it('renders nothing while loading', () => {
    mockUseUserStore.mockReturnValue({ isAuthenticated: false, isLoading: true });

    const { container } = renderWithRouter(
      <ProtectedRoute><div>Protected Content</div></ProtectedRoute>,
    );

    expect(container.innerHTML).toBe('');
    expect(screen.queryByText('Protected Content')).not.toBeInTheDocument();
  });

  it('renders children when authenticated', () => {
    mockUseUserStore.mockReturnValue({ isAuthenticated: true, isLoading: false });

    renderWithRouter(
      <ProtectedRoute><div>Protected Content</div></ProtectedRoute>,
    );

    expect(screen.getByText('Protected Content')).toBeInTheDocument();
  });

  it('redirects to /auth when not authenticated', () => {
    mockUseUserStore.mockReturnValue({ isAuthenticated: false, isLoading: false });

    renderWithRouter(
      <ProtectedRoute><div>Protected Content</div></ProtectedRoute>,
    );

    expect(screen.queryByText('Protected Content')).not.toBeInTheDocument();
  });
});
