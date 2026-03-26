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
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Separator } from '@/components/ui/separator'
import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from '@/components/ui/accordion'

interface User {
  id: number
  email: string
  role: string
}

interface UsersResponse {
  data: User[]
  meta: { total: number; offset: number; limit: number }
}

const accordionTriggerClass =
  'py-3 text-sm hover:no-underline font-display tracking-wide [&_[data-slot=accordion-trigger-icon]]:text-primary/80'

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
  const defaultOpen =
    isLoading || users.length === 0 ? (['invite'] as string[]) : (['directory'] as string[])

  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="font-display text-lg tracking-wide">Users</CardTitle>
        <CardDescription>
          Invites are one-time links tied to an email. The directory lists everyone who can sign in.
        </CardDescription>
      </CardHeader>
      <CardContent className="pt-0">
        <Accordion
          key={isLoading ? 'loading' : users.length === 0 ? 'empty' : 'full'}
          multiple
          defaultValue={defaultOpen}
          className="w-full"
        >
          <AccordionItem value="invite" className="border-border/50 dark:border-white/10">
            <AccordionTrigger className={accordionTriggerClass}>
              Invite by email
            </AccordionTrigger>
            <AccordionContent className="space-y-4 pb-4">
              <div className="space-y-2">
                <Label htmlFor="invite-email" className="text-xs text-muted-foreground">
                  Email of the person you&apos;re inviting
                </Label>
                <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
                  <Input
                    id="invite-email"
                    type="email"
                    placeholder="user@example.com"
                    value={inviteEmail}
                    onChange={(e) => setInviteEmail(e.target.value)}
                    className="h-9 w-full min-w-0 sm:flex-1"
                  />
                  <Button
                    size="lg"
                    className="h-9 w-full shrink-0 px-4 sm:w-auto"
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
                  <Separator />
                  <div className="space-y-2">
                    {inviteEmail && (
                      <p className="text-xs text-muted-foreground">
                        Invite for{' '}
                        <span className="font-medium text-foreground">{inviteEmail}</span>
                      </p>
                    )}
                    <Label className="text-xs text-muted-foreground">One-time link</Label>
                    <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
                      <Input
                        readOnly
                        value={inviteLink}
                        className="h-9 min-h-0 font-mono text-xs sm:min-w-0 sm:flex-1"
                      />
                      <Button
                        size="lg"
                        variant="outline"
                        className="h-9 w-full shrink-0 border-primary/35 text-primary hover:bg-primary/10 sm:w-auto"
                        onClick={handleCopyLink}
                      >
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
                  <Button
                    size="sm"
                    variant="ghost"
                    onClick={() => {
                      setInviteLink(null)
                      setInviteEmail('')
                    }}
                  >
                    Dismiss
                  </Button>
                </>
              )}
            </AccordionContent>
          </AccordionItem>

          <AccordionItem value="directory" className="border-border/50 dark:border-white/10">
            <AccordionTrigger className={accordionTriggerClass}>
              People
              {!isLoading && (
                <span className="ml-2 font-mono text-xs font-normal text-muted-foreground tabular-nums">
                  ({users.length})
                </span>
              )}
            </AccordionTrigger>
            <AccordionContent className="pb-4">
              {isLoading ? (
                <div className="rounded-lg border border-dashed border-border/60 py-10 text-center text-sm text-muted-foreground">
                  Loading…
                </div>
              ) : users.length === 0 ? (
                <div className="rounded-lg border border-dashed border-border/60 px-4 py-10 text-center text-sm text-muted-foreground">
                  No accounts yet. Open &quot;Invite by email&quot; above to add someone.
                </div>
              ) : (
                <ScrollArea className="h-[min(52vh,440px)] rounded-lg border border-border/60 dark:border-white/10">
                  <div className="divide-y divide-border/60 dark:divide-white/10">
                    {users.map((user) => (
                      <div
                        key={user.id}
                        className="flex items-center justify-between gap-4 px-4 py-3 hover:bg-muted/25 transition-colors"
                      >
                        <div className="flex min-w-0 items-center gap-3">
                          <span className="truncate font-medium">{user.email}</span>
                          <Badge variant="secondary" className="shrink-0 font-mono text-xs">
                            {user.role}
                          </Badge>
                        </div>
                        <DropdownMenu>
                          <DropdownMenuTrigger
                            className="inline-flex size-8 shrink-0 cursor-pointer items-center justify-center rounded-lg outline-none transition-colors hover:bg-muted/50 focus-visible:ring-2 focus-visible:ring-ring"
                            aria-label={`Actions for ${user.email}`}
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
                </ScrollArea>
              )}
            </AccordionContent>
          </AccordionItem>
        </Accordion>
      </CardContent>
    </Card>
  )
}
