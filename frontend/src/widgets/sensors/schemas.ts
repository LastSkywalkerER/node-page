import { z } from 'zod';

export const temperatureStatSchema = z.object({
  sensor_key: z.string(),
  temperature: z.number(),
  high: z.number().optional().default(0),
  critical: z.number().optional().default(0),
});

export const temperatureMetricSchema = z.object({
  timestamp: z.string(),
  sensors: z.array(temperatureStatSchema),
});

export const sensorsResponseSchema = z.object({
  sensors: temperatureMetricSchema,
});

export type TemperatureStat = z.infer<typeof temperatureStatSchema>;
export type TemperatureMetric = z.infer<typeof temperatureMetricSchema>;
export type SensorsResponse = z.infer<typeof sensorsResponseSchema>;


