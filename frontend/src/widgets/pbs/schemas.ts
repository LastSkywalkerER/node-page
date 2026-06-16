import { z } from 'zod'

export const PBSDatastoreSchema = z.object({
  store: z.string(),
  total: z.number().optional().default(0),
  used: z.number().optional().default(0),
  avail: z.number().optional().default(0),
  usage_percent: z.number().optional().default(0),
  error: z.string().optional().default(''),
})

export const PBSTaskSchema = z.object({
  type: z.string(),
  id: z.string().optional().default(''),
  start_time: z.number().optional().default(0),
  status: z.string().optional().default(''),
})

export const PBSBackupHealthSchema = z.object({
  last_backup_at: z.number().optional().default(0),
  last_error_at: z.number().optional().default(0),
  last_error: z.string().optional().default(''),
  running_tasks: z.number().optional().default(0),
  recent_tasks: z.array(PBSTaskSchema).optional().default([]),
})

export const PBSStatusSchema = z.object({
  host_id: z.number(),
  datastores: z.array(PBSDatastoreSchema).optional().default([]),
  backups: PBSBackupHealthSchema.optional().default({}),
  updated_at: z.string().optional().default(''),
})

export type PBSStatus = z.infer<typeof PBSStatusSchema>
export type PBSDatastore = z.infer<typeof PBSDatastoreSchema>
export type PBSTask = z.infer<typeof PBSTaskSchema>
