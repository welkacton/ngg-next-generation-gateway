// Live verification of the full native Xray config editing path.
const fs = require('fs')
const cfgText = fs.readFileSync('configs/ngg.yaml', 'utf8')
const TOKEN = cfgText.split('\n').find((l) => l.includes('token:')).split('token:')[1].trim()
const BASE = 'http://127.0.0.1:9090/api/v1'
async function api(method, p, body) {
  const o = { method, headers: { Authorization: 'Bearer ' + TOKEN } }
  if (body !== undefined) { o.headers['Content-Type'] = 'application/json'; o.body = JSON.stringify(body) }
  const r = await fetch(BASE + p, o)
  let j; try { j = await r.json() } catch (e) { j = { raw: await r.text() } }
  return { status: r.status, j }
}
const sample3x = JSON.parse(fs.readFileSync('configs/examples/3x-ui-default.sample.json', 'utf8'))
const sampleRW = JSON.parse(fs.readFileSync('configs/examples/remnawave-xray.sample.json', 'utf8'))
const content3x = JSON.stringify(sample3x, null, 2)
const contentRW = JSON.stringify(sampleRW, null, 2)

;(async () => {
  for (let i = 0; i < 30; i++) {
    try { const r = await api('GET', '/health'); if (r.status === 200) break } catch (e) {}
    await new Promise((r) => setTimeout(r, 1000))
  }

  const tpl = await api('GET', '/nodes/templates')
  console.log('templates:', tpl.status, tpl.j.map((t) => t.id).join(', '))

  const p3 = await api('POST', '/nodes/parse', { kernel: 'xray-core', content: content3x })
  console.log('parse 3X-UI:', p3.status, 'nodes=' + p3.j.nodes?.length,
    'detected=' + JSON.stringify(p3.j.detected), 'warnings=' + (p3.j.warnings || []).length)

  const pRW = await api('POST', '/nodes/parse', { kernel: 'xray-core', content: contentRW })
  console.log('parse Remnawave:', pRW.status, 'nodes=' + pRW.j.nodes?.length,
    'routing=' + pRW.j.routing?.length,
    'matchers=' + JSON.stringify((pRW.j.routing || []).map((r) => r.matchers || [])))

  const imp = await api('POST', '/nodes/from-native', { kernel: 'xray-core', content: content3x })
  console.log('import from native:', imp.status, 'imported=' + imp.j.imported)

  // Native validation via the kernel's own validator (no file changes).
  const val = await api('POST', '/cores/xray-core/config/validate', { content: content3x })
  console.log('kernel native validate:', val.status, 'ok=' + val.j.ok)

  // Full apply path: backup -> native validate -> replace, then restore.
  const before = await api('GET', '/cores/xray-core/config')
  const applied = await api('PUT', '/cores/xray-core/config', { content: content3x, reason: 'native-e2e' })
  console.log('apply native config:', applied.status, 'ok=' + applied.j.ok,
    'backup=' + (applied.j.backup && applied.j.backup.id))
  const after = await api('GET', '/cores/xray-core/config')
  console.log('config actually changed:', before.j.sha256 !== after.j.sha256)
  const restored = await api('PUT', '/cores/xray-core/config', { content: before.j.content, reason: 'native-e2e-restore' })
  console.log('restored original:', restored.status, 'ok=' + restored.j.ok)

  // Clean up imported nodes so the delivered state stays pristine.
  await api('PUT', '/nodes/import', { nodes: [] })
  const finalNodes = await api('GET', '/nodes')
  const health = await api('GET', '/health')
  console.log('final nodes =', finalNodes.j.count, '| kernels running =', health.j.cores_running)
  console.log('DONE')
})().catch((e) => { console.error('E2E ERROR', e); process.exit(1) })
