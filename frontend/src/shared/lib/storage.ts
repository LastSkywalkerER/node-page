// Storage service for managing authentication tokens and user data
class StorageService {
  private readonly ACCESS_TOKEN_KEY = 'app:access';
  private readonly REFRESH_TOKEN_KEY = 'app:refresh';
  private readonly ACCESS_EXP_KEY = 'app:accessExp';
  private readonly REFRESH_EXP_KEY = 'app:refreshExp';
  private readonly USER_KEY = 'app:user';

  // Access token methods
  getAccessToken(): string | null {
    return localStorage.getItem(this.ACCESS_TOKEN_KEY);
  }

  setAccessToken(token: string, expUnix: number): void {
    localStorage.setItem(this.ACCESS_TOKEN_KEY, token);
    localStorage.setItem(this.ACCESS_EXP_KEY, expUnix.toString());
  }

  getAccessTokenExp(): number | null {
    const exp = localStorage.getItem(this.ACCESS_EXP_KEY);
    return exp ? parseInt(exp, 10) : null;
  }

  // Refresh token methods
  getRefreshToken(): string | null {
    return localStorage.getItem(this.REFRESH_TOKEN_KEY);
  }

  setRefreshToken(token: string, expUnix: number): void {
    localStorage.setItem(this.REFRESH_TOKEN_KEY, token);
    localStorage.setItem(this.REFRESH_EXP_KEY, expUnix.toString());
  }

  getRefreshTokenExp(): number | null {
    const exp = localStorage.getItem(this.REFRESH_EXP_KEY);
    return exp ? parseInt(exp, 10) : null;
  }

  // User data methods
  getUser(): any | null {
    const userStr = localStorage.getItem(this.USER_KEY);
    return userStr ? JSON.parse(userStr) : null;
  }

  setUser(user: any): void {
    localStorage.setItem(this.USER_KEY, JSON.stringify(user));
  }

  // Utility methods
  isAccessTokenExpired(): boolean {
    const exp = this.getAccessTokenExp();
    if (!exp) return true;
    return Date.now() >= exp * 1000;
  }

  isRefreshTokenExpired(): boolean {
    const exp = this.getRefreshTokenExp();
    if (!exp) return true;
    return Date.now() >= exp * 1000;
  }

  hasValidTokens(): boolean {
    return !this.isAccessTokenExpired() && !this.isRefreshTokenExpired();
  }

  clearAll(): void {
    localStorage.removeItem(this.ACCESS_TOKEN_KEY);
    localStorage.removeItem(this.REFRESH_TOKEN_KEY);
    localStorage.removeItem(this.ACCESS_EXP_KEY);
    localStorage.removeItem(this.REFRESH_EXP_KEY);
    localStorage.removeItem(this.USER_KEY);
  }

  // Get token for API requests
  getAuthToken(): string | null {
    if (this.isAccessTokenExpired()) {
      return null;
    }
    return this.getAccessToken();
  }
}

export const storageService = new StorageService();

