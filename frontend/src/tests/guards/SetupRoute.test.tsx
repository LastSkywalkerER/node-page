import { render, screen } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { MemoryRouter } from 'react-router-dom';
import { SetupRoute } from '../../shared/guards/SetupRoute';

const mockUseSetupStatus = vi.fn();
vi.mock('../../widgets/setup/useSetup', () => ({
  useSetupStatus: () => mockUseSetupStatus(),
}));

function renderWithRouter(ui: React.ReactElement) {
  return render(<MemoryRouter initialEntries={['/']}>{ui}</MemoryRouter>);
}

describe('SetupRoute', () => {
  beforeEach(() => vi.clearAllMocks());

  it('shows loading state while checking setup status', () => {
    mockUseSetupStatus.mockReturnValue({ data: undefined, isLoading: true });

    renderWithRouter(<SetupRoute><div>App Content</div></SetupRoute>);

    expect(screen.getByText('Loading...')).toBeInTheDocument();
    expect(screen.queryByText('App Content')).not.toBeInTheDocument();
  });

  it('renders children when setup is not needed', () => {
    mockUseSetupStatus.mockReturnValue({ data: { setup_needed: false }, isLoading: false });

    renderWithRouter(<SetupRoute><div>App Content</div></SetupRoute>);

    expect(screen.getByText('App Content')).toBeInTheDocument();
  });

  it('redirects to /setup when setup is needed', () => {
    mockUseSetupStatus.mockReturnValue({ data: { setup_needed: true }, isLoading: false });

    renderWithRouter(<SetupRoute><div>App Content</div></SetupRoute>);

    expect(screen.queryByText('App Content')).not.toBeInTheDocument();
  });
});
