import { Outlet } from 'react-router-dom'

export function AdminPage() {
  return (
    <div className="mx-auto max-w-4xl px-4 py-8 md:px-6 md:py-10">
      <Outlet />
    </div>
  )
}
