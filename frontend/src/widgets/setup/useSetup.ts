import { useQuery, useMutation, UseQueryOptions } from '@tanstack/react-query';
import { apiClient } from '../../shared/lib/api';
import { 
  SetupStatusResponse, 
  ConfigResponse, 
  CompleteSetupResponse,
  CompleteSetupFormData 
} from './schemas';

/**
 * Hook to check if setup is needed
 */
export function useSetupStatus() {
  return useQuery<SetupStatusResponse>({
    queryKey: ['setup', 'status'],
    queryFn: async () => {
      const response = await apiClient.get<{ data: SetupStatusResponse }>('/setup/status');
      return response.data.data;
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
        config: {
          jwt_secret: data.config.jwt_secret,
          refresh_secret: data.config.refresh_secret,
          addr: data.config.addr || ':8080',
          gin_mode: data.config.gin_mode || 'release',
          debug: data.config.debug || 'false',
          db_type: data.config.db_type || 'sqlite',
          db_dsn: data.config.db_dsn || 'stats.db',
        },
        admin_email: data.admin_email,
        admin_password: data.admin_password,
      });
      return response.data.data;
    },
  });
}

