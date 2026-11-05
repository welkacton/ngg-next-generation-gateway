#!/usr/bin/env node
/**
 * NGG release helper — the mandatory "modify -> sync client -> bump version" step.
 *
 *   node scripts/release.mjs 2.3.0                       # bump + rebuild client + backend + tests
 *   node scripts/release.mjs 2.3.0 --sync                # also deploy to the installed Windows client
 *   node scripts/release.mjs 2.3.0 --no-build            # only rewrite version strings
 *   node scripts/release.mjs 2.3.0 --publish-links --no-build
 *       # AFTER the GitHub Release is actually published: update download /
 *       # deploy examples to the new tag without touching historical tables.
 *
 * Version has ONE source of truth: internal/version/version.go (Release).
 * This script propagates it to the web client package and every doc that
 * quotes the CURRENT release, then rebuilds the client bundle so the browser
 * can never serve stale UI with a new version badge.
 */
import fs from 'node:fs'
import path from 'node:path'
import { spawnSync } from 'node:child_process'
import { fileURLToPath } from 'node:url'

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const next = process.argv[2]
const NO_BUILD = process.argv.includes('--no-build')
const SYNC = process.argv.includes('--sync')
const PUBLISH_LINKS = process.argv.includes('--publish-links')

if (!/^\d+\.\d+\.\d+$/.test(next || '')) {
  console.error('usage: node scripts/release.mjs <x.y.z> [--sync] [--no-build]')
  process.exit(1)
}

const versionGo = path.join(ROOT, 'internal/version/version.go')
const src = fs.readFileSync(versionGo, 'utf8')
const match = /Release\s*=\s*"([^"]+)"/.exec(src)
if (!match) {
  console.error('cannot find Release = "..." in internal/version/version.go')
  process.exit(1)
}
const current = match[1]

// Only "current product version" strings are rewritten. Deployment guides pin
// the LATEST PUBLISHED GitHub Release tag (e.g. --version v2.2.2); bumping
// those before a Release exists would point at assets that do not exist.
// They are updated manually when scripts/deploy-vps.sh / release.yml publish
// a new tag. Historical "introduced in vX.Y.Z" tables are never rewritten.
const VERSION_FILES = [
  'internal/version/version.go',
  'web/package.json',
  'README.md',
  'docs/10-updates.md',
  'docs/02-api.md',
]

function rewrite(rel, text) {
  if (rel === 'internal/version/version.go') {
    return text.replace(/(Release\s*=\s*")[^"]+(")/, `$1${next}$2`)
  }
  if (rel === 'web/package.json') {
    return text.replace(/("version"\s*:\s*")[^"]+(")/, `$1${next}$2`)
  }
  if (rel === 'README.md') {
    return text
      .replace(/(### .*Release )v\d+\.\d+\.\d+/, `$1v${next}`)
      .replace(/(Gen 2 · )v\d+\.\d+\.\d+/g, `$1v${next}`)
  }
  if (rel === 'docs/10-updates.md') {
    return text.replace(/(Gen 2 · )v\d+\.\d+\.\d+/g, `$1v${next}`)
  }
  if (rel === 'docs/02-api.md') {
    return text
      .replace(/("ngg_version"\s*:\s*")[^"]+(")/, `$1${next}$2`)
      .replace(/(Gen 2 \/ )v\d+\.\d+\.\d+/g, `$1v${next}`)
  }
  return text
}

console.log(`NGG release: v${current} -> v${next}`)
let changed = 0
for (const rel of VERSION_FILES) {
  const file = path.join(ROOT, rel)
  if (!fs.existsSync(file)) continue
  const text = fs.readFileSync(file, 'utf8')
  const out = rewrite(rel, text)
  if (out === text) continue
  fs.writeFileSync(file, out, 'utf8')
  console.log(`  updated ${rel}`)
  changed += 1
}
if (changed === 0 && current !== next) {
  console.error(`no file contained the current version ${current}`)
  process.exit(1)
}

// Deployment/download examples must only move to a tag that REALLY exists.
// Run this after the Release workflow has published its assets. The patterns
// are prefix-anchored so historical notes such as "v2.1.1 及以上" survive.
const PUBLISH_LINK_FILES = [
  'README.md',
  'docs/DEPLOY.md',
  'docs/DEPLOY-VPS.md',
  'docs/INSTALL-WINDOWS.md',
  'docs/10-updates.md',
]
// [pattern, replacement] pairs: match the WHOLE version-bearing token, so no
// capture-group callback is needed (and offsets can never leak into text).
const LINK_PATTERNS = [
  [/--version v\d+\.\d+\.\d+/g, `--version v${next}`],
  [/--release v\d+\.\d+\.\d+/g, `--release v${next}`],
  [/releases\/tag\/v\d+\.\d+\.\d+/g, `releases/tag/v${next}`],
  [/releases\/download\/v\d+\.\d+\.\d+/g, `releases/download/v${next}`],
  [/NGG-Setup-v\d+\.\d+\.\d+/g, `NGG-Setup-v${next}`],
  [/NGG-v\d+\.\d+\.\d+-windows-amd64/g, `NGG-v${next}-windows-amd64`],
  [/ngg-v\d+\.\d+\.\d+-(linux|darwin|windows|freebsd)/g, `ngg-v${next}-$1`],
  [/VERSION=v\d+\.\d+\.\d+/g, `VERSION=v${next}`],
  [/-Version v\d+\.\d+\.\d+/g, `-Version v${next}`],
  [/"tag":"v\d+\.\d+\.\d+"/g, `"tag":"v${next}"`],
  [/"tag_name": "v\d+\.\d+\.\d+"/g, `"tag_name": "v${next}"`],
  [/"latest": "v\d+\.\d+\.\d+"/g, `"latest": "v${next}"`],
  [/升到 v\d+\.\d+\.\d+/g, `升到 v${next}`],
]
function rewritePublishLinks(rel, text) {
  const transform = (line) =>
    LINK_PATTERNS.reduce((acc, [re, replacement]) => acc.replace(re, replacement), line)
  if (rel === 'README.md') {
    return text
      .split('\n')
      .map((line) => (/^\|\s*(Gen 2|Gen 1|\*\*Gen 1\*\*)/.test(line) ? line : transform(line)))
      .join('\n')
  }
  return transform(text)
}
if (PUBLISH_LINKS) {
  for (const rel of PUBLISH_LINK_FILES) {
    const file = path.join(ROOT, rel)
    if (!fs.existsSync(file)) continue
    const text = fs.readFileSync(file, 'utf8')
    const out = rewritePublishLinks(rel, text)
    if (out !== text) {
      fs.writeFileSync(file, out, 'utf8')
      console.log(`  published links -> v${next} in ${rel}`)
    }
  }
}

if (NO_BUILD) {
  console.log('version strings updated (--no-build: client not rebuilt)')
  process.exit(0)
}

function run(cmd, args, cwd = ROOT) {
  const exe = process.platform === 'win32' && cmd === 'npm' ? 'npm.cmd' : cmd
  console.log(`\n$ ${cmd} ${args.join(' ')}`)
  const res = spawnSync(exe, args, { cwd, stdio: 'inherit', shell: process.platform === 'win32' })
  if (res.error) throw res.error
  if (res.status !== 0) {
    console.error(`FAILED: ${cmd} ${args.join(' ')} (exit ${res.status})`)
    process.exit(res.status || 1)
  }
}

const goBin = process.env.NGG_GO_BINARY || 'go'
const dist = path.join(ROOT, 'web/dist')
// Vite's emptyOutDir uses fs.rmSync, which some desktop sandboxes intercept
// with a trash helper that times out. Removing via the platform shell keeps
// the build deterministic on both Windows and POSIX.
if (fs.existsSync(dist)) {
  const rm = process.platform === 'win32'
    ? spawnSync('cmd.exe', ['/c', 'rmdir', '/s', '/q', dist], { stdio: 'ignore' })
    : spawnSync('rm', ['-rf', dist], { stdio: 'ignore' })
  if (rm.status !== 0) {
    console.error('could not remove stale web/dist; remove it manually and retry')
    process.exit(1)
  }
  console.log('removed stale web/dist')
}

run('npm', ['run', 'build'], path.join(ROOT, 'web'))
run(goBin, ['build', './...'])
run(goBin, ['vet', './...'])
run(goBin, ['test', '-p', '2', './...', '-count=1'])
run(goBin, ['build', '-trimpath', '-o', process.platform === 'win32' ? 'bin/nggd.exe' : 'bin/nggd', './cmd/nggd'])

if (SYNC) {
  if (process.platform !== 'win32') {
    console.log('--sync is Windows-only; copy web/dist and bin/nggd manually')
  } else {
    run('powershell.exe', ['-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', 'scripts/sync-client.ps1'])
  }
}

console.log(`\nOK: v${next} built. Client bundle: web/dist. Backend: bin/nggd(.exe).`)
console.log('Next: git add -A && git commit && git push, then tag the release.')
