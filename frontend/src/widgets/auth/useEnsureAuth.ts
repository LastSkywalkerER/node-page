import { useEffect, useRef } from 'react';
import { AxiosError } from 'axios';
import { useGetMe } from './useGetMe';
import { useRefresh } from './useRefresh';
import { useUserStore } from '../../shared/store/user';

export function useEnsureAuth() {
  const hasTriedRefreshRef = useRef(false);
  const { isAuthenticated, hasValidTokens, getAccessToken } = useUserStore();

  const refreshMutation = useRefresh();
  // Only enable getMe if we have valid tokens
  // Check tokens on each render to ensure we have them before making the request
  const shouldFetchMe = hasValidTokens() && !!getAccessToken();
  const getMeQuery = useGetMe({ enabled: shouldFetchMe });

  useEffect(() => {
    if (!getMeQuery.error) return;
    const err = getMeQuery.error as AxiosError;
    const status = err.response?.status;
    if (status === 401 && !hasTriedRefreshRef.current) {
      hasTriedRefreshRef.current = true;
      refreshMutation
        .mutateAsync()
        .then(() => {
          // After successful refresh, attempt to get user again
          getMeQuery.refetch();
        })
        .catch(() => {
          // Refresh failed: nothing to do here; store was cleared in hook
        });
    }
  }, [getMeQuery.error, refreshMutation, getMeQuery]);

  const loading = getMeQuery.isLoading || refreshMutation.isPending;

  return {
    isLoading: loading,
    isAuthenticated,
  };
}

