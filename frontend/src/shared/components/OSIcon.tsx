import {
  siAlmalinux,
  siAlpinelinux,
  siApple,
  siArchlinux,
  siCentos,
  siDebian,
  siFedora,
  siFreebsd,
  siGentoo,
  siKalilinux,
  siLinux,
  siLinuxmint,
  siManjaro,
  siNixos,
  siOpensuse,
  siPopos,
  siProxmox,
  siRaspberrypi,
  siRedhat,
  siRockylinux,
  siUbuntu,
} from 'simple-icons'
import { Server } from 'lucide-react'
import { cn } from '@/lib/utils'

interface BrandIcon {
  title: string
  path: string
}

// simple-icons dropped Microsoft brands — the classic four-pane logo is
// trivial to inline (24x24 viewBox, same contract as the rest).
const windowsIcon: BrandIcon = {
  title: 'Windows',
  path: 'M0 0h11.377v11.372H0V0zm12.623 0H24v11.372H12.623V0zM0 12.623h11.377V24H0V12.623zm12.623 0H24V24H12.623V12.623z',
}

const platformIcons: Record<string, BrandIcon> = {
  proxmox: siProxmox,
  ubuntu: siUbuntu,
  debian: siDebian,
  devuan: siDebian,
  raspbian: siRaspberrypi,
  alpine: siAlpinelinux,
  fedora: siFedora,
  centos: siCentos,
  rhel: siRedhat,
  redhat: siRedhat,
  rocky: siRockylinux,
  almalinux: siAlmalinux,
  arch: siArchlinux,
  archlinux: siArchlinux,
  manjaro: siManjaro,
  nixos: siNixos,
  opensuse: siOpensuse,
  'opensuse-leap': siOpensuse,
  'opensuse-tumbleweed': siOpensuse,
  suse: siOpensuse,
  gentoo: siGentoo,
  kali: siKalilinux,
  linuxmint: siLinuxmint,
  mint: siLinuxmint,
  pop: siPopos,
  freebsd: siFreebsd,
  darwin: siApple,
  macos: siApple,
  linux: siLinux,
}

interface OSIconHost {
  host_type?: string
  platform?: string
  platform_family?: string
  os?: string
}

/**
 * Resolves the brand icon for a host: Proxmox for hypervisor rows, then the
 * distro from `platform` (gopsutil values and PVE ostype map to the same
 * names), then the OS family. Null = no brand match (caller falls back).
 */
function resolveBrandIcon(host: OSIconHost): BrandIcon | null {
  if (host.host_type === 'hypervisor') return siProxmox
  for (const raw of [host.platform, host.platform_family, host.os]) {
    const key = (raw ?? '').trim().toLowerCase()
    if (!key) continue
    if (key.startsWith('win') || key.startsWith('w2k') || key === 'wxp' || key === 'windows') {
      return windowsIcon
    }
    const icon = platformIcons[key]
    if (icon) return icon
  }
  return null
}

/**
 * OS / platform icon for machine cards, guest rows and breadcrumbs.
 * Renders in currentColor so it follows the theme; callers set the color
 * via className (defaults to muted).
 */
export function OSIcon({ host, className }: { host: OSIconHost; className?: string }) {
  const icon = resolveBrandIcon(host)
  if (!icon) {
    return <Server className={cn('shrink-0', className)} aria-hidden />
  }
  return (
    <svg
      viewBox="0 0 24 24"
      fill="currentColor"
      role="img"
      aria-label={icon.title}
      className={cn('shrink-0', className)}
    >
      <title>{icon.title}</title>
      <path d={icon.path} />
    </svg>
  )
}
