import { useQuery } from '@tanstack/react-query';

export function useSensors(hostId?: number | null) {
  const queryKey = hostId ? ['sensors', hostId] : ['sensors'];

  return useQuery({
    queryKey,
    queryFn: async () => {
      const url = hostId ? `/api/sensors?host_id=${hostId}` : '/api/sensors';
      const response = await fetch(url);
      if (!response.ok) throw new Error('Failed to fetch sensors');
      return response.json();
    },
    refetchInterval: 5000,
    staleTime: 1000,
  });
}


