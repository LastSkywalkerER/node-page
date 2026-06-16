import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { isAxiosError } from 'axios';
import { apiClient } from '@/shared/lib/api';
import {
  ConnectorsListSchema,
  ConnectorPreviewSchema,
  type ConnectorsList,
  type ConnectorPreview,
  type Connector,
  type ConnectRequest,
} from './schemas';

/** Backend error envelope → readable message for toasts. */
function apiError(e: unknown): Error {
  if (isAxiosError(e)) {
    const data = e.response?.data as { error?: string } | undefined;
    if (data?.error) return new Error(data.error);
  }
  return e instanceof Error ? e : new Error(String(e));
}

/**
 * Connectors registry (admin-only endpoint — gate with `enabled`).
 * Discovered hints + configured connectors with poller status.
 */
export function useConnectors(enabled = true) {
  return useQuery<ConnectorsList>({
    queryKey: ['connectors'],
    queryFn: async () => {
      const { data } = await apiClient.get('/connectors');
      return ConnectorsListSchema.parse(data);
    },
    enabled,
    refetchInterval: 30000,
    staleTime: 10000,
  });
}

export function useTestProxmox() {
  return useMutation<ConnectorPreview, Error, ConnectRequest>({
    mutationFn: async (req) => {
      try {
        const { data } = await apiClient.post('/connectors/proxmox/test', req);
        return ConnectorPreviewSchema.parse(data.preview);
      } catch (e) {
        throw apiError(e);
      }
    },
  });
}

export function useCreateProxmox() {
  const queryClient = useQueryClient();
  return useMutation<Connector, Error, ConnectRequest>({
    mutationFn: async (req) => {
      try {
        const { data } = await apiClient.post('/connectors', req);
        return data.connector as Connector;
      } catch (e) {
        throw apiError(e);
      }
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['connectors'] });
      queryClient.invalidateQueries({ queryKey: ['hosts'] });
    },
  });
}

export function useTestPBS() {
  return useMutation<ConnectorPreview, Error, ConnectRequest>({
    mutationFn: async (req) => {
      try {
        const { data } = await apiClient.post('/connectors/pbs/test', req);
        return ConnectorPreviewSchema.parse(data.preview);
      } catch (e) {
        throw apiError(e);
      }
    },
  });
}

export function useCreatePBS() {
  const queryClient = useQueryClient();
  return useMutation<Connector, Error, ConnectRequest>({
    mutationFn: async (req) => {
      try {
        const { data } = await apiClient.post('/connectors/pbs', req);
        return data.connector as Connector;
      } catch (e) {
        throw apiError(e);
      }
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['connectors'] });
      queryClient.invalidateQueries({ queryKey: ['hosts'] });
    },
  });
}

export function useSavePexels() {
  const queryClient = useQueryClient();
  return useMutation<Connector, Error, { api_key: string; query: string }>({
    mutationFn: async (req) => {
      try {
        const { data } = await apiClient.post('/connectors/pexels', req);
        return data.connector as Connector;
      } catch (e) {
        throw apiError(e);
      }
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['connectors'] });
      queryClient.invalidateQueries({ queryKey: ['wallpaper'] });
    },
  });
}

export function useToggleConnector() {
  const queryClient = useQueryClient();
  return useMutation<Connector, Error, { id: number; enabled: boolean }>({
    mutationFn: async ({ id, enabled }) => {
      try {
        const { data } = await apiClient.patch(`/connectors/${id}`, { enabled });
        return data.connector as Connector;
      } catch (e) {
        throw apiError(e);
      }
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['connectors'] }),
  });
}

export function useDeleteConnector() {
  const queryClient = useQueryClient();
  return useMutation<void, Error, { id: number; removeHosts: boolean }>({
    mutationFn: async ({ id, removeHosts }) => {
      try {
        await apiClient.delete(`/connectors/${id}`, { params: { remove_hosts: removeHosts } });
      } catch (e) {
        throw apiError(e);
      }
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['connectors'] });
      queryClient.invalidateQueries({ queryKey: ['hosts'] });
    },
  });
}

export function useSyncConnector() {
  const queryClient = useQueryClient();
  return useMutation<void, Error, number>({
    mutationFn: async (id) => {
      try {
        await apiClient.post(`/connectors/${id}/sync`);
      } catch (e) {
        throw apiError(e);
      }
    },
    onSuccess: () => {
      // The poller picks the trigger up within its cycle; refresh shortly after.
      setTimeout(() => {
        queryClient.invalidateQueries({ queryKey: ['connectors'] });
        queryClient.invalidateQueries({ queryKey: ['hosts'] });
      }, 2000);
    },
  });
}

const DISMISS_KEY = 'connectors:dismissed-hints';

/** Hint dismissal is per-browser (localStorage) — the Connectors tab always lists hints regardless. */
export function dismissedHints(): string[] {
  try {
    return JSON.parse(localStorage.getItem(DISMISS_KEY) ?? '[]');
  } catch {
    return [];
  }
}

export function dismissHint(type: string) {
  const cur = dismissedHints();
  if (!cur.includes(type)) {
    localStorage.setItem(DISMISS_KEY, JSON.stringify([...cur, type]));
  }
}
