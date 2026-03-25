import { renderHook, waitFor } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { createElement } from 'react';
import { useLogin } from '../../widgets/auth/useLogin';

const mockNavigate = vi.fn();
vi.mock('react-router-dom', () => ({ useNavigate: () => mockNavigate }));

const mockSetAuthFromResponse = vi.fn();
vi.mock('../../shared/store/user', () => ({
  useUserStore: () => ({ setAuthFromResponse: mockSetAuthFromResponse }),
}));

const mockLogin = vi.fn();
vi.mock('../../shared/lib/auth', () => ({
  authService: { login: (...args: unknown[]) => mockLogin(...args) },
}));

function createWrapper() {
  const qc = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
  return ({ children }: { children: React.ReactNode }) =>
    createElement(QueryClientProvider, { client: qc }, children);
}

describe('useLogin', () => {
  beforeEach(() => vi.clearAllMocks());

  it('calls authService.login and navigates on success', async () => {
    const payload = { user: { id: 1, email: 'a@b.com', role: 'admin' }, expires_in: 3600 };
    mockLogin.mockResolvedValueOnce(payload);

    const { result } = renderHook(() => useLogin(), { wrapper: createWrapper() });
    result.current.mutate({ email: 'a@b.com', password: 'secret' });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(mockLogin).toHaveBeenCalledWith('a@b.com', 'secret');
    expect(mockSetAuthFromResponse).toHaveBeenCalledWith(payload);
    expect(mockNavigate).toHaveBeenCalledWith('/machines');
  });

  it('sets error state on login failure', async () => {
    mockLogin.mockRejectedValueOnce(new Error('Invalid credentials'));

    const { result } = renderHook(() => useLogin(), { wrapper: createWrapper() });
    result.current.mutate({ email: 'a@b.com', password: 'wrong' });

    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(mockNavigate).not.toHaveBeenCalled();
    expect(mockSetAuthFromResponse).not.toHaveBeenCalled();
  });
});
