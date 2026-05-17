import { useQuery, useMutation, UseQueryOptions } from '@tanstack/react-query';
import { apiClient } from '../../shared/lib/api';
import {
  SetupStatusResponse,
  ConfigResponse,
  CompleteSetupResponse,
  CompleteSetupFormData,
  toSetupConfigApiPayload,
} from './schemas';

/**
 * Hook to check if setup is needed
 */
export function useSetupStatus() {
  return useQuery<SetupStatusResponse>({
    queryKey: ['setup', 'status'],
    queryFn: async () => {
      const response = await apiClient.get<{ data: SetupStatusResponse }>('/setup/status');
      const d = response.data.data;
      return {
        ...d,
        machine_hints: d.machine_hints ?? { suggested_hostname: '', suggested_ipv4: '' },
      };
    },
    retry: false,
    refetchOnWindowFocus: false,
  });
}

/**
 * Hook to get current configuration values
 */
export function useSetupConfig(options?: Omit<UseQueryOptions<ConfigResponse>, 'queryKey' | 'queryFn'>) {
  return useQuery<ConfigResponse>({
    queryKey: ['setup', 'config'],
    queryFn: async () => {
      const response = await apiClient.get<{ data: ConfigResponse }>('/setup/config');
      return response.data.data;
    },
    retry: false,
    refetchOnWindowFocus: false,
    ...options,
  });
}

/**
 * Hook to complete setup
 */
export function useCompleteSetup() {
  return useMutation<CompleteSetupResponse, Error, CompleteSetupFormData>({
    mutationFn: async (data: CompleteSetupFormData) => {
      const response = await apiClient.post<{ data: CompleteSetupResponse }>('/setup/complete', {
        config: toSetupConfigApiPayload(data.config),
        admin_email: data.admin_email,
        admin_password: data.admin_password,
      });
      return response.data.data;
    },
  });
}

/**
 * Fetches the exact .env file body the server will write (for the review step).
 */
export function useSetupEnvPreview(config: CompleteSetupFormData['config'] | null, enabled: boolean) {
  return useQuery({
    queryKey: ['setup', 'preview-env', config],
    enabled: enabled && config !== null,
    staleTime: Infinity,
    gcTime: 5 * 60 * 1000,
    queryFn: async () => {
      const response = await apiClient.post<{ data: { content: string } }>('/setup/preview-env', {
        config: toSetupConfigApiPayload(config!),
      });
      return response.data.data.content;
    },
  });
}

/**
 * Posts the wizard's "join an existing cluster" choice. Returns once the
 * peer has accepted us as a voter; replication itself runs in the
 * background and is observed by polling /setup/status.
 */
export interface JoinRaftClusterInput {
  peer_url: string;
  token: string;
  node_id?: string;
  bind_addr?: string;
  advertise_addr?: string;
  advertise_url?: string;
  data_dir?: string;
  bridge_shared_secret?: string;
  bridge_remote_seeds?: string[];
}

export interface JoinRaftClusterResponse {
  message: string;
  peer_response: unknown;
}

export function useJoinRaftCluster() {
  return useMutation<JoinRaftClusterResponse, Error, JoinRaftClusterInput>({
    mutationFn: async (input) => {
      const response = await apiClient.post<{ data: JoinRaftClusterResponse }>(
        '/setup/join-raft-cluster',
        input,
      );
      return response.data.data;
    },
  });
}

/**
 * Public, no-auth Raft progress polling for the join flow's
 * "Waiting for snapshot..." state. Exposes the same shape as the
 * authenticated /raft/status but trimmed to the fields the wizard
 * needs to render progress + diagnose stalls.
 */
export interface RaftProgress {
  enabled: boolean;
  state?: string;
  leader_id?: string;
  applied_index: number;
  commit_index: number;
  last_index: number;
  node_id?: string;
  cluster_id?: string;
}

export function useRaftProgress(enabled: boolean) {
  return useQuery<RaftProgress>({
    queryKey: ['setup', 'raft-progress'],
    queryFn: async () => {
      const resp = await apiClient.get<{ data: RaftProgress }>('/setup/raft-progress');
      return resp.data.data;
    },
    enabled,
    refetchInterval: enabled ? 2000 : false,
    retry: false,
    refetchOnWindowFocus: false,
  });
}

/**
 * Public, no-auth TCP-reachability probe for the join flow's
 * "Waiting for snapshot..." state. The backend tries to TCP-connect
 * to addr from this server and reports success / failure. Used to
 * diagnose "the leader can't reach my advertise address" mid-join.
 */
export interface CheckReachableResponse {
  reachable: boolean;
  addr: string;
  error?: string;
}
export function useCheckReachable() {
  return useMutation<CheckReachableResponse, Error, string>({
    mutationFn: async (addr) => {
      const response = await apiClient.post<{ data: CheckReachableResponse }>(
        '/setup/check-reachable',
        { addr },
      );
      return response.data.data;
    },
  });
}

