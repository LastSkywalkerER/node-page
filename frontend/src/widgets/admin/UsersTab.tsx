import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiClient } from '@/shared/lib/api'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { UserPlus, MoreHorizontal, Copy, Check } from 'lucide-react'
import { toast } from 'sonner'

interface User {
  id: number
  email: string
  role: string
}

interface UsersResponse {
  data: User[]
  meta: { total: number; offset: number; limit: number }
}

export function UsersTab() {
  const [inviteEmail, setInviteEmail] = useState('')
  const [inviteLink, setInviteLink] = useState<string | null>(null)
  const [copied, setCopied] = useState(false)
  const queryClient = useQueryClient()

  const { data, isLoading } = useQuery({
    queryKey: ['users'],
    queryFn: async () => {
      const res = await apiClient.get<UsersResponse>('/users?limit=100')
      return res.data
    },
  })

  const createInviteMutation = useMutation({
    mutationFn: async () => {
      const res = await apiClient.post<{ data: { link: string; token: string } }>('/invitations', {
        email: inviteEmail.trim(),
      })
      return res.data.data
    },
    onSuccess: (result) => {
      setInviteLink(result.link)
    },
    onError: (err: Error & { response?: { data?: { error?: string } } }) => {
      toast.error(err.response?.data?.error || err.message || 'Failed to create invitation')
    },
  })

  const updateRoleMutation = useMutation({
    mutationFn: async ({ id, role }: { id: number; role: string }) => {
      await apiClient.patch(`/users/${id}`, { role })
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['users'] })
      toast.success('Role updated')
    },
    onError: (err: Error & { response?: { data?: { error?: string } } }) => {
      toast.error(err.response?.data?.error || err.message || 'Failed to update role')
    },
  })

  const deleteUserMutation = useMutation({
    mutationFn: async (id: number) => {
      await apiClient.delete(`/users/${id}`)
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['users'] })
      toast.success('User deleted')
    },
    onError: (err: Error & { response?: { data?: { error?: string } } }) => {
      toast.error(err.response?.data?.error || err.message || 'Failed to delete user')
    },
  })

  const handleCopyLink = async () => {
    if (!inviteLink) return
    try {
      await navigator.clipboard.writeText(inviteLink)
      setCopied(true)
      toast.success('Link copied to clipboard')
      setTimeout(() => setCopied(false), 2000)
    } catch {
      toast.error('Failed to copy')
    }
  }

  const users = data?.data ?? []

  return (
    <div className="space-y-6">
      <h2 className="text-lg font-semibold">Users</h2>

      {/* Unified invite block */}
      <div className="rounded-xl border bg-card p-5 space-y-4">
        <h3 className="font-medium text-sm">Invite user</h3>
        <div className="space-y-2">
          <Label htmlFor="invite-email" className="text-xs text-muted-foreground">
            Email of the person you're inviting
          </Label>
          <div className="flex gap-2 flex-wrap">
            <Input
              id="invite-email"
              type="email"
              placeholder="user@example.com"
              value={inviteEmail}
              onChange={(e) => setInviteEmail(e.target.value)}
              className="max-w-xs"
            />
            <Button
              size="sm"
              onClick={() => createInviteMutation.mutate()}
              disabled={createInviteMutation.isPending || !inviteEmail.trim()}
            >
              <UserPlus className="h-4 w-4 mr-2" />
              Generate link
            </Button>
          </div>
          <p className="text-xs text-muted-foreground">
            They must use this exact email when registering.
          </p>
        </div>

        {inviteLink && (
          <>
            <div className="border-t border-border pt-4 space-y-2">
              {inviteEmail && (
                <p className="text-xs text-muted-foreground">
                  Invite for <span className="font-medium text-foreground">{inviteEmail}</span>
                </p>
              )}
              <Label className="text-xs text-muted-foreground">One-time link</Label>
              <div className="flex gap-2">
                <Input
                  readOnly
                  value={inviteLink}
                  className="font-mono text-xs flex-1 min-w-0"
                />
                <Button size="sm" variant="outline" onClick={handleCopyLink} className="shrink-0">
                  {copied ? (
                    <>
                      <Check className="h-4 w-4 mr-1.5" />
                      Copied
                    </>
                  ) : (
                    <>
                      <Copy className="h-4 w-4 mr-1.5" />
                      Copy
                    </>
                  )}
                </Button>
              </div>
            </div>
            <Button size="sm" variant="ghost" onClick={() => { setInviteLink(null); setInviteEmail('') }}>
              Dismiss
            </Button>
          </>
        )}
      </div>

      {isLoading ? (
        <div className="text-sm text-muted-foreground py-8">Loading users...</div>
      ) : users.length === 0 ? (
        <div className="text-sm text-muted-foreground py-8">No users yet. Invite someone to get started.</div>
      ) : (
        <div className="border rounded-xl divide-y overflow-hidden">
          {users.map((user) => (
            <div
              key={user.id}
              className="flex items-center justify-between gap-4 px-4 py-3 hover:bg-muted/30 transition-colors"
            >
              <div className="flex items-center gap-3 min-w-0">
                <span className="font-medium truncate">{user.email}</span>
                <Badge variant="secondary" className="text-xs shrink-0">
                  {user.role}
                </Badge>
              </div>
              <DropdownMenu>
                <DropdownMenuTrigger
                  className="inline-flex size-8 shrink-0 items-center justify-center rounded-lg hover:bg-muted transition-colors"
                >
                  <MoreHorizontal className="h-4 w-4" />
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end">
                  <DropdownMenuItem
                    onClick={() => updateRoleMutation.mutate({ id: user.id, role: 'ADMIN' })}
                    disabled={user.role === 'ADMIN'}
                  >
                    Set as Admin
                  </DropdownMenuItem>
                  <DropdownMenuItem
                    onClick={() => updateRoleMutation.mutate({ id: user.id, role: 'USER' })}
                    disabled={user.role === 'USER'}
                  >
                    Set as User
                  </DropdownMenuItem>
                  <DropdownMenuItem
                    className="text-destructive"
                    onClick={() => deleteUserMutation.mutate(user.id)}
                  >
                    Delete
                  </DropdownMenuItem>
                </DropdownMenuContent>
              </DropdownMenu>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
