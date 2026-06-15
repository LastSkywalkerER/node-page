import { Outlet } from 'react-router-dom'
import { AdminSidebar } from '@/widgets/admin/AdminSidebar'

export function AdminPage() {
  return (
    <div className="mx-auto max-w-5xl px-4 py-6 md:px-6 md:py-8">
      <div className="flex flex-col gap-4 md:flex-row md:gap-6">
        <AdminSidebar />
        <div className="min-w-0 flex-1">
          <Outlet />
        </div>
      </div>
    </div>
  )
}
