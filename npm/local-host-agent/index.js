#!/usr/bin/env node

const fs = require('node:fs')
const os = require('node:os')
const path = require('node:path')
const crypto = require('node:crypto')
const http = require('node:http')
const https = require('node:https')
const zlib = require('node:zlib')
const { spawn } = require('node:child_process')

const packageVersion = require('./package.json').version
const MAX_ARCHIVE_BYTES = 128 * 1024 * 1024
const MAX_BINARY_BYTES = 256 * 1024 * 1024
const REQUEST_TIMEOUT_MS = 15_000
const LOCK_TIMEOUT_MS = 30_000

function targetName() {
  const platform = process.platform
  const arch = process.arch
  if (platform === 'linux' && arch === 'x64') return 'linux-x64'
  if (platform === 'linux' && arch === 'arm64') return 'linux-arm64'
  if (platform === 'win32') {
    throw new Error('native Windows is not supported by the Incus host agent; run the Linux binary inside WSL and configure your MCP client to launch it there')
  }
  throw new Error(`unsupported local host-agent target: ${platform}/${arch}; supported targets are Linux x64 and arm64 (Windows via WSL)`)
}

function request(url, redirects = 0) {
  if (redirects > 5) return Promise.reject(new Error('too many artifact redirects'))
  return new Promise((resolve, reject) => {
    let parsed
    try { parsed = new URL(url) } catch { reject(new Error(`invalid artifact URL: ${url}`)); return }
    const transport = parsed.protocol === 'http:' ? http : parsed.protocol === 'https:' ? https : null
    if (!transport) { reject(new Error(`unsupported artifact URL protocol: ${parsed.protocol}`)); return }
    const req = transport.get(parsed, { headers: { 'User-Agent': '@opute/local-host-agent' } }, response => {
      if ([301, 302, 303, 307, 308].includes(response.statusCode) && response.headers.location) {
        response.resume()
        resolve(request(new URL(response.headers.location, parsed).toString(), redirects + 1))
        return
      }
      if (response.statusCode !== 200) {
        response.resume()
        reject(new Error(`host-agent download failed: HTTP ${response.statusCode}`))
        return
      }
      const declared = Number(response.headers['content-length'] || 0)
      if (declared > MAX_ARCHIVE_BYTES) {
        response.resume()
        reject(new Error('host-agent download exceeds the maximum artifact size'))
        return
      }
      const chunks = []
      let size = 0
      response.on('data', chunk => {
        size += chunk.length
        if (size > MAX_ARCHIVE_BYTES) {
          req.destroy(new Error('host-agent download exceeds the maximum artifact size'))
          return
        }
        chunks.push(chunk)
      })
      response.on('end', () => resolve(Buffer.concat(chunks)))
      response.on('error', reject)
    })
    req.setTimeout(REQUEST_TIMEOUT_MS, () => req.destroy(new Error('host-agent download timed out')))
    req.on('error', reject)
  })
}

function sha256(data) {
  return crypto.createHash('sha256').update(data).digest('hex')
}

async function withCacheLock(lockPath, fn) {
  const deadline = Date.now() + LOCK_TIMEOUT_MS
  while (true) {
    try {
      fs.mkdirSync(lockPath, { recursive: false, mode: 0o700 })
      try { return await fn() } finally { fs.rmSync(lockPath, { recursive: true, force: true }) }
    } catch (error) {
      if (error && error.code !== 'EEXIST') throw error
      if (Date.now() >= deadline) throw new Error('timed out waiting for another host-agent download')
      await new Promise(resolve => setTimeout(resolve, 100))
    }
  }
}

function readCacheMarker(markerPath, binaryPath, expected) {
  try {
    const marker = JSON.parse(fs.readFileSync(markerPath, 'utf8'))
    if (marker.archiveSha256 !== expected) return false
    const binary = fs.readFileSync(binaryPath)
    if (binary.length === 0 || binary.length > MAX_BINARY_BYTES) return false
    return marker.binarySha256 === sha256(binary)
  } catch {
    return false
  }
}

async function resolveBinary() {
  if (process.env.OPUTE_HOST_AGENT_BINARY) return process.env.OPUTE_HOST_AGENT_BINARY
  const target = targetName()
  const cacheDir = process.env.OPUTE_HOST_AGENT_CACHE_DIR || path.join(os.homedir(), '.cache', 'opute', 'host-agent')
  const binaryName = `host-agent-${target}`
  const binaryPath = path.join(cacheDir, packageVersion, binaryName)
  const markerPath = `${binaryPath}.verified.json`
  const lockPath = path.join(cacheDir, `${packageVersion}.lock`)
  const base = (process.env.OPUTE_HOST_AGENT_RELEASE_BASE_URL || 'https://github.com/wunderous/host-agents/releases/download').replace(/\/$/, '')
  const artifactName = `host-agent-${target}.gz`
  const expected = process.env.OPUTE_HOST_AGENT_SHA256
    ? process.env.OPUTE_HOST_AGENT_SHA256.toLowerCase().trim()
    : await resolveChecksum(base, artifactName)
  if (!/^[a-f0-9]{64}$/.test(expected)) throw new Error('OPUTE_HOST_AGENT_SHA256 must be a 64-character hexadecimal SHA-256')

  fs.mkdirSync(cacheDir, { recursive: true, mode: 0o700 })

  return withCacheLock(lockPath, async () => {
    if (readCacheMarker(markerPath, binaryPath, expected)) return binaryPath
    const archive = await request(`${base}/v${packageVersion}/${artifactName}`)
    const actualArchive = sha256(archive)
    if (actualArchive !== expected) throw new Error('host-agent artifact checksum mismatch')
    let binary
    try { binary = zlib.gunzipSync(archive) } catch { throw new Error('host-agent artifact is not valid gzip') }
    if (binary.length === 0 || binary.length > MAX_BINARY_BYTES) throw new Error('host-agent binary exceeds the maximum size')
    fs.mkdirSync(path.dirname(binaryPath), { recursive: true, mode: 0o700 })
    const temporary = `${binaryPath}.download-${process.pid}-${Date.now()}`
    const temporaryMarker = `${markerPath}.download-${process.pid}-${Date.now()}`
    try {
      fs.writeFileSync(temporary, binary, { mode: 0o700 })
      fs.writeFileSync(temporaryMarker, JSON.stringify({ archiveSha256: expected, binarySha256: sha256(binary) }) + '\n', { mode: 0o600 })
      fs.renameSync(temporary, binaryPath)
      fs.renameSync(temporaryMarker, markerPath)
    } catch (error) {
      fs.rmSync(temporary, { force: true })
      fs.rmSync(temporaryMarker, { force: true })
      throw error
    }
    return binaryPath
  })
}

async function resolveChecksum(base, artifactName) {
  const manifestUrl = process.env.OPUTE_HOST_AGENT_CHECKSUM_URL || `${base}/v${packageVersion}/SHA256SUMS`
  const manifest = (await request(manifestUrl)).toString('utf8')
  for (const line of manifest.split(/\r?\n/)) {
    const match = line.trim().match(/^([a-f0-9]{64})\s+[* ]?(.+)$/i)
    if (match && path.basename(match[2]) === artifactName) return match[1].toLowerCase()
  }
  throw new Error(`release checksum manifest does not contain ${artifactName}`)
}

async function main() {
  const binary = await resolveBinary()
  const child = spawn(binary, ['--mode=standalone', '--transport=stdio', ...process.argv.slice(2)], {
    stdio: 'inherit',
    env: { ...process.env, OPUTE_AGENT_MODE: 'standalone', OPUTE_TRANSPORT: 'stdio' },
  })
  let exiting = false
  for (const signal of ['SIGINT', 'SIGTERM', 'SIGHUP']) {
    process.on(signal, () => { if (!exiting) child.kill(signal) })
  }
  child.on('error', error => {
    console.error(`failed to start host agent: ${error.message}`)
    process.exitCode = 1
  })
  child.on('exit', (code, signal) => {
    exiting = true
    process.exitCode = code ?? (signal ? 1 : 0)
  })
}

main().catch(error => {
  console.error(error.message)
  process.exitCode = 1
})
