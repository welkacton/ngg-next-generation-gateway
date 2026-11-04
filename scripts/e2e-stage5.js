// NGG Stage 2-5 live API verification against the running daemon.
const fs = require('fs')
const cfg = fs.readFileSync('configs/ngg.yaml', 'utf8')
const TOKEN = cfg.split('\n').find((l) => l.includes('token:')).split('token:')[1].trim()
const BASE = 'http://127.0.0.1:9090/api/v1'
async function api(method, p, body) {
  const o = { method, headers: { Authorization: 'Bearer ' + TOKEN } }
  if (body !== undefined) { o.headers['Content-Type'] = 'application/json'; o.body = JSON.stringify(body) }
  const r = await fetch(BASE + p, o)
  let j; try { j = await r.json() } catch (e) { j = { raw: await r.text() } }
  return { status: r.status, j }
}
const show = (k, r, pick) => {
  let v = r.j
  if (pick) v = pick(r.j)
  console.log(k.padEnd(28), r.status, JSON.stringify(v).slice(0, 220))
}
;(async () => {
  console.log('--- Stage 3: rules / DNS ---')
  show('rules/load', await api('POST', '/network/rules/load', { path: 'configs/examples/rules-sample.yaml' }))
  show('rules/list', await api('GET', '/network/rules'), (j) => ({ count: j.rules.length, default_action: j.default_action }))
  show('rules/add', await api('POST', '/network/rules', { id: 'live-test', type: 'DOMAIN-SUFFIX', value: '.live.test', action: 'PROXY', core_id: 'mihomo', priority: 70, enabled: true }))
  show('rules/delete', await api('DELETE', '/network/rules/live-test'))
  show('dns/status', await api('GET', '/network/dns'))
  show('dns/hosts', await api('POST', '/network/dns/hosts', { hosts: { 'ngg.local.': '10.89.0.1' }, block: ['blocked.example.'] }))
  show('dns/resolve(local)', await api('POST', '/network/dns/resolve', { name: 'ngg.local.', type: 'A' }))
  show('packettest', await api('POST', '/network/packettest'))

  console.log('\n--- Stage 4: protocols / PQ / plugins ---')
  show('protocols', await api('GET', '/protocols'), (j) => j.length + ' protocols')
  show('protocol/wireguard', await api('GET', '/protocols/wireguard'), (j) => ({ id: j.id, kernels: j.kernels }))
  show('protocol/config', await api('POST', '/protocols/wireguard/config', { kernel: 'sing-box', params: { server: '198.51.100.1', port: 51820, private_key: 'x' } }), (j) => ({ ok: j.ok, format: j.format, validated: j.validated }))
  show('pq/status', await api('GET', '/pq/status'), (j) => j.suite)
  show('pq/selftest', await api('POST', '/pq/selftest'))
  show('plugins/install-js', await api('POST', '/plugins', {
    id: 'live-js', name: 'Live JS router', kind: 'javascript', hooks: ['session', 'dns'],
    permissions: ['decision:write'], enabled: true,
    source: 'var result={}; if(ngg.input.domain && ngg.input.domain.indexOf("video")>=0){result.core_id="sing-box";result.tags=["video"];} if(ngg.input.name==="bad.example"){result.block=true;}',
  }))
  show('plugins/list', await api('GET', '/plugins'), (j) => j.map((p) => p.manifest.id + ':' + p.loaded).join(','))
  show('plugins/test', await api('POST', '/plugins/live-js/test', { input: { domain: 'video.example.com', hook: 'session' } }))
  show('plugins/dns-hook', await api('POST', '/plugins/live-js/test', { input: { domain: 'bad.example', hook: 'dns' } }))

  console.log('\n--- Stage 5: SD-WAN / tenants / platforms ---')
  show('sdwan/add-links', await api('POST', '/sdwan/links', { id: 'wan1', name: 'Primary', probe_target: '127.0.0.1:9090', cost: 1, enabled: true }))
  await api('POST', '/sdwan/links', { id: 'wan2', name: 'Backup', probe_target: '127.0.0.1:18080', cost: 2, enabled: true })
  show('sdwan/start', await api('POST', '/sdwan/start'))
  await new Promise((r) => setTimeout(r, 800))
  show('sdwan/status', await api('GET', '/sdwan/status'), (j) => ({ policy: j.policy, active: j.active, links: j.links.map((l) => l.id + ':' + l.healthy) }))
  show('sdwan/failover', await api('POST', '/sdwan/failover'), (j) => ({ active: j.active && j.active.id }))
  show('tenants/add', await api('POST', '/tenants', { id: 'acme', name: 'Acme', enabled: true, allowed_cores: ['mihomo'], quota_bytes: 1000000 }))
  show('tenants/list', await api('GET', '/tenants'), (j) => j.map((t) => t.id).join(','))
  show('platforms', await api('GET', '/platforms'), (j) => ({ host: j.host, targets: j.targets.length, clients: j.clients.length }))
  show('stages', await api('GET', '/stages'), (j) => Object.keys(j).map((k) => k + '=' + j[k].status).join(' '))
  console.log('\nDONE')
})().catch((e) => { console.error('E2E ERROR', e); process.exit(1) })
