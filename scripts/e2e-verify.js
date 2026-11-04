// NGG Phase 1 end-to-end verification against the REAL running service.
// Usage: node scripts/e2e-verify.js   (nggd must already be running on :9090)
// All assertions use live OS processes, native validators and real sockets.
const fs = require('fs')
const path = require('path')

const cfg = fs.readFileSync(path.join(__dirname, '../configs/ngg.yaml'), 'utf8')
const tokenLine = cfg.split('\n').find((l) => l.includes('token:'))
const TOKEN = tokenLine.split('token:')[1].trim()
const BASE = 'http://127.0.0.1:9090/api/v1'

async function api(method, urlPath, body) {
  const opt = { method, headers: { Authorization: 'Bearer ' + TOKEN } }
  if (body !== undefined) {
    opt.headers['Content-Type'] = 'application/json'
    opt.body = JSON.stringify(body)
  }
  const r = await fetch(BASE + urlPath, opt)
  let j
  try { j = await r.json() } catch (e) { j = { raw: await r.text() } }
  return { status: r.status, j }
}

;(async () => {
  console.log('--- 1. REAL LOGS ---')
  const logs = (await api('GET', '/cores/mihomo/logs?limit=8')).j
  console.log('mihomo log lines:', logs.length)
  logs.slice(-2).forEach((x) => console.log('   [' + x.stream + '] ' + x.line.slice(0, 100)))
  const xlogs = (await api('GET', '/cores/xray-core/logs?limit=8')).j
  console.log('xray log lines:', xlogs.length)
  xlogs.slice(-1).forEach((x) => console.log('   [' + x.stream + '] ' + x.line.slice(0, 100)))

  console.log('\n--- 2. CONFIG TRANSACTION (real native validator) ---')
  const before = (await api('GET', '/cores/mihomo/config')).j
  const valid = before.content + '\n# NGG e2e verification marker\n'
  const v1 = await api('POST', '/cores/mihomo/config/validate', { content: valid })
  console.log('validate VALID   ->', v1.status, 'ok=' + v1.j.ok)
  console.log('   native output:', (v1.j.output || '').split('\n').slice(-1)[0].slice(0, 100))
  const applied = await api('PUT', '/cores/mihomo/config', { content: valid, reason: 'e2e-valid' })
  console.log('apply VALID      ->', applied.status, 'ok=' + applied.j.ok, 'backup=' + (applied.j.backup && applied.j.backup.id))
  const afterValid = (await api('GET', '/cores/mihomo/config')).j
  console.log('config changed   ->', afterValid.sha256 !== before.sha256, before.sha256.slice(0, 12), '->', afterValid.sha256.slice(0, 12))

  const invalid = 'this is not: [valid yaml\n  -\n'
  const v2 = await api('POST', '/cores/mihomo/config/validate', { content: invalid })
  console.log('validate INVALID ->', v2.status, 'ok=' + v2.j.ok)
  const rejected = await api('PUT', '/cores/mihomo/config', { content: invalid, reason: 'e2e-invalid' })
  console.log('apply INVALID    ->', rejected.status, (rejected.j.error || '').slice(0, 140))
  const afterRejected = (await api('GET', '/cores/mihomo/config')).j
  console.log('FAIL-CLOSED OK   ->', afterRejected.sha256 === afterValid.sha256 ? 'YES (effective config byte-identical)' : 'NO *** BUG ***')

  console.log('\n--- 3. BACKUPS + ROLLBACK ---')
  const backups = (await api('GET', '/cores/mihomo/config/backups')).j
  console.log('backups recorded:', backups.length, backups.map((b) => b.reason).join(','))
  const rb = await api('POST', '/cores/mihomo/config/rollback', { backup_id: backups[0].id })
  console.log('rollback         ->', rb.status, rb.j.ok === true ? 'OK' : JSON.stringify(rb.j))
  const afterRollback = (await api('GET', '/cores/mihomo/config')).j
  console.log('restored sha256  ->', afterRollback.sha256 === before.sha256 ? 'YES (matches original)' : 'NO *** BUG ***')

  console.log('\n--- 4. PORT CONFLICT DETECTION (real sockets) ---')
  const ports = (await api('GET', '/ports')).j
  const mihomoPorts = ports.find((p) => p.core_id === 'mihomo').ports
  const chk = await api('GET', '/ports/check?ports=' + mihomoPorts.join(','))
  console.log('mihomo ports', mihomoPorts.join(','), '-> occupied now:', JSON.stringify(chk.j.conflicts))

  console.log('\n--- 5. WEBSOCKET EVENT STREAM ---')
  const wsResult = await new Promise((resolve) => {
    let ws
    try { ws = new WebSocket('ws://127.0.0.1:9090/api/v1/ws?token=' + TOKEN) } catch (e) { return resolve('unavailable: ' + e.message) }
    const types = new Set()
    let count = 0
    setTimeout(() => { try { ws.close() } catch (e) {} resolve([...types].join(',') + ' (' + count + ' events)') }, 7000)
    ws.onmessage = (ev) => { try { const e = JSON.parse(ev.data); types.add(e.type); count++ } catch (e) {} }
    ws.onerror = () => {}
  })
  console.log('WS events received:', wsResult)

  console.log('\n--- 6. AUDIT TRAIL ---')
  const audit = (await api('GET', '/audit?limit=8')).j
  console.log(audit.map((a) => a.action + '/' + (a.core_id || '-')).join(' | '))

  console.log('\n--- 7. REGISTRY + STATIC UI ---')
  const reg = (await api('GET', '/registry')).j
  console.log('registered kernels:', reg.map((d) => d.id + '(' + d.source + ')').join(', '))
  const reload = await api('POST', '/registry/reload', {})
  console.log('registry reload  ->', JSON.stringify(reload.j))
  const idx = await fetch('http://127.0.0.1:9090/')
  console.log('UI index         ->', idx.status, idx.headers.get('content-type'))
})().catch((e) => { console.error('E2E ERROR', e); process.exit(1) })
