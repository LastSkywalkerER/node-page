import { describe, it, expect } from 'vitest'
import { nodeAlertTarget, nodeAlertSummary, NODE_SETTINGS_PATH } from '@/widgets/hosts/nodeAlert'
import type { Host, NodeAlert } from '@/widgets/hosts/schemas'

const alert = (over: Partial<NodeAlert> = {}): NodeAlert => ({
  kind: 'raft_isolated',
  severity: 'error',
  title: 'Node advertises an address it no longer has',
  detail: '',
  action: 'Give the machine 192.168.0.110 back, or point the node at 192.168.0.103.',
  fix: 'readvertise',
  fix_target: '192.168.0.103:7000',
  node_id: 'skynas',
  advertise_addr: '192.168.0.110:7000',
  advertise_url: 'http://192.168.0.110:9090',
  local_ipv4: '192.168.0.103',
  node_url: 'http://192.168.0.103:9090',
  since: '',
  ...over,
})

const host = (over: Partial<Host> = {}): Host =>
  ({ id: 9, name: 'SkyNAS', dashboard_url: '', node_alert: null, ...over }) as Host

describe('nodeAlertTarget', () => {
  it('routes inside this app when the fault is on this very node', () => {
    expect(nodeAlertTarget(host({ id: 1, node_alert: alert() }))).toEqual({ local: true })
    // An explicit local host id wins over the id=1 assumption.
    expect(nodeAlertTarget(host({ id: 7, node_alert: alert() }), 7)).toEqual({ local: true })
  })

  it("opens the settings of the OTHER machine, through the URL it answers on now", () => {
    const t = nodeAlertTarget(host({ node_alert: alert(), dashboard_url: 'http://192.168.0.110:9090' }), 1)
    // Not the registered dashboard_url: that is the stale address being
    // complained about, so it would land nowhere.
    expect(t).toEqual({ href: 'http://192.168.0.103:9090' + NODE_SETTINGS_PATH })
  })

  it('falls back to the registered dashboard URL when the alert carries none', () => {
    const t = nodeAlertTarget(host({ node_alert: alert({ node_url: '' }), dashboard_url: 'https://sky.example.com/' }), 1)
    expect(t).toEqual({ href: 'https://sky.example.com' + NODE_SETTINGS_PATH })
  })

  it('routes locally rather than nowhere when no URL is known', () => {
    expect(nodeAlertTarget(host({ node_alert: alert({ node_url: '' }) }), 1)).toEqual({ local: true })
  })
})

describe('nodeAlertSummary', () => {
  it('states the fault and what to do about it', () => {
    expect(nodeAlertSummary(host({ node_alert: alert() }))).toBe(
      'Node advertises an address it no longer has — Give the machine 192.168.0.110 back, or point the node at 192.168.0.103.'
    )
  })

  it('keeps the headline when there is no action, and says nothing when healthy', () => {
    expect(nodeAlertSummary(host({ node_alert: alert({ action: '' }) }))).toBe(
      'Node advertises an address it no longer has'
    )
    expect(nodeAlertSummary(host())).toBe('')
  })
})
