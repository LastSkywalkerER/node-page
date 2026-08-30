import { describe, it, expect, vi, beforeEach } from 'vitest';

const mockPost = vi.fn();
const mockGet = vi.fn();

vi.mock('../../shared/store/user', () => ({
  useUserStore: { getState: () => ({ scheduleTokenRefresh: vi.fn(), clearAuth: vi.fn() }) },
}));

vi.mock('axios', () => ({
  default: {
    create: () => ({
      post: (...args: unknown[]) => mockPost(...args),
      get: (...args: unknown[]) => mockGet(...args),
      interceptors: { response: { use: vi.fn() } },
    }),
  },
  AxiosError: class extends Error {},
}));

const { authService } = await import('../../shared/lib/auth');

function response(data: unknown, status = 200, contentType = 'application/json') {
  return { data, status, headers: { 'content-type': contentType } };
}

describe('authService response handling', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.spyOn(console, 'error').mockImplementation(() => {});
  });

  it('unwraps the {data:{...}} envelope', async () => {
    const payload = { user: { id: 1, email: 'a@b.com', role: 'admin' }, expires_in: 3600 };
    mockPost.mockResolvedValueOnce(response({ data: payload }));
    await expect(authService.login('a@b.com', 'secret')).resolves.toEqual(payload);
  });

  // A proxy / captive portal / stale cache answering 200 with HTML used to
  // surface as "Cannot read properties of undefined (reading 'user')".
  it('reports an HTML body instead of throwing a TypeError', async () => {
    mockPost.mockResolvedValueOnce(response('<!doctype html><html></html>', 200, 'text/html'));
    await expect(authService.login('a@b.com', 'secret')).rejects.toThrow(
      /Unexpected server response to sign-in \(HTTP 200, text\/html\)/,
    );
  });

  it('reports an envelope that carries no user', async () => {
    mockPost.mockResolvedValueOnce(response({ data: { expires_in: 3600 } }));
    await expect(authService.login('a@b.com', 'secret')).rejects.toThrow(/carried no user/);
  });

  it('reports an empty body', async () => {
    mockPost.mockResolvedValueOnce(response('', 200, 'text/plain'));
    await expect(authService.login('a@b.com', 'secret')).rejects.toThrow(
      /Unexpected server response to sign-in/,
    );
  });
});
