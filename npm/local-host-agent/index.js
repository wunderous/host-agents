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
const HEALTH_WAIT_MS = 15_000

function targetName() {
  const platform = process.platform
  const arch = process.arch
  if (platform === 'linux' && arch === 'x64') return 'linux-x64'
  if (platform === 'linux' && arch === 'arm64') return 'linux-arm64'
  if (platform === 'win32') {
    throw new Error('native Windows is not supported by the Incus host agent; run the Linux binary inside WSL and point your MCP client at http://127.0.0.1:3014/mcp')
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

function runtimeDir() {
  return process.env.OPUTE_LOCAL_HOST_AGENT_RUNTIME_DIR
    || path.join(os.homedir(), '.cache', 'opute', 'host-agent', 'standalone')
}

function daemonStatePath() {
  return path.join(runtimeDir(), 'daemon.json')
}

function bindHost() {
  return (process.env.HOST_MCP_BIND_HOST || '127.0.0.1').trim() || '127.0.0.1'
}

function mcpPort() {
  const raw = (process.env.HOST_MCP_PORT || '3014').trim()
  const port = Number(raw)
  if (!Number.isInteger(port) || port <= 0 || port > 65535) {
    throw new Error(`invalid HOST_MCP_PORT ${JSON.stringify(raw)}`)
  }
  return port
}

function mcpUrl(host = bindHost(), port = mcpPort()) {
  return `http://${host}:${port}/mcp`
}

function healthUrl(host = bindHost(), port = mcpPort()) {
  return `http://${host}:${port}/health`
}

function readDaemonState() {
  try {
    return JSON.parse(fs.readFileSync(daemonStatePath(), 'utf8'))
  } catch {
    return null
  }
}

function writeDaemonState(state) {
  fs.mkdirSync(runtimeDir(), { recursive: true, mode: 0o700 })
  fs.writeFileSync(daemonStatePath(), JSON.stringify(state, null, 2) + '\n', { mode: 0o600 })
}

function clearDaemonState() {
  fs.rmSync(daemonStatePath(), { force: true })
}

function isPidRunning(pid) {
  if (!Number.isInteger(pid) || pid <= 0) return false
  try {
    process.kill(pid, 0)
    return true
  } catch {
    return false
  }
}

function probeHealth(url, timeoutMs = 2000) {
  return new Promise(resolve => {
    const req = http.get(url, { timeout: timeoutMs }, response => {
      let body = ''
      response.on('data', chunk => { body += chunk })
      response.on('end', () => {
        resolve(response.statusCode === 200 && body.includes('"ok"'))
      })
    })
    req.on('timeout', () => { req.destroy(); resolve(false) })
    req.on('error', () => resolve(false))
  })
}

async function waitForHealth(url, timeoutMs = HEALTH_WAIT_MS, failure = null) {
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    const earlyFailure = typeof failure === 'function' ? failure() : null
    if (earlyFailure) throw new Error(earlyFailure)
    if (await probeHealth(url)) return
    await new Promise(resolve => setTimeout(resolve, 100))
  }
  throw new Error(`timed out waiting for health at ${url}`)
}

function buildAgentEnv() {
  const env = { ...process.env }
  for (const key of Object.keys(env)) {
    if (key.startsWith('OPUTE_') || key === 'MCP_AUTH_TOKEN' || key === 'BRIDGE_TOKEN') {
      // Keep launcher-only and standalone-safe vars.
      if (
        key === 'OPUTE_HOST_AGENT_BINARY'
        || key === 'OPUTE_HOST_AGENT_CACHE_DIR'
        || key === 'OPUTE_HOST_AGENT_RELEASE_BASE_URL'
        || key === 'OPUTE_HOST_AGENT_CHECKSUM_URL'
        || key === 'OPUTE_HOST_AGENT_SHA256'
        || key === 'OPUTE_LOCAL_HOST_AGENT_RUNTIME_DIR'
        || key === 'OPUTE_STANDALONE_STATE_DIR'
        || key === 'OPUTE_STANDALONE_ALLOW_MUTATIONS'
        || key === 'OPUTE_STANDALONE_ALLOW_INSECURE_DOWNLOADS'
        || key === 'OPUTE_INFRA_PROVIDER_ID'
        || key === 'HOST_MCP_PORT'
        || key === 'HOST_MCP_BIND_HOST'
      ) {
        continue
      }
      delete env[key]
    }
  }
  env.OPUTE_AGENT_MODE = 'standalone'
  env.OPUTE_TRANSPORT = 'http'
  env.HOST_MCP_BIND_HOST = bindHost()
  env.HOST_MCP_PORT = String(mcpPort())
  if (!env.OPUTE_INFRA_PROVIDER_ID) env.OPUTE_INFRA_PROVIDER_ID = 'incus'
  return env
}

function printUsage() {
  console.log(`Usage: opute-local-host-agent <command>

Commands:
  start [--background|-d]  Start standalone Streamable HTTP MCP (default: foreground)
  stop                     Stop a background daemon started by this launcher
  status                   Show daemon/listener status and MCP URL
  url                      Print the MCP URL (http://127.0.0.1:3014/mcp by default)

Environment:
  HOST_MCP_PORT            Listen port (default 3014)
  HOST_MCP_BIND_HOST       Bind host (default 127.0.0.1)
  OPUTE_HOST_AGENT_BINARY  Use a local binary instead of downloading a release
  OPUTE_STANDALONE_ALLOW_MUTATIONS=true  Enable mutating tools

MCP client config:
  { "type": "http", "url": "http://127.0.0.1:3014/mcp" }
`)
}

async function startForeground(binary, passthroughArgs) {
  const host = bindHost()
  const port = mcpPort()
  const url = mcpUrl(host, port)
  console.error(`starting standalone Streamable HTTP MCP at ${url}`)
  const child = spawn(binary, ['--mode=standalone', '--transport=http', ...passthroughArgs], {
    stdio: 'inherit',
    env: buildAgentEnv(),
  })
  let exiting = false
  for (const signal of ['SIGINT', 'SIGTERM', 'SIGHUP']) {
    process.on(signal, () => { if (!exiting) child.kill(signal) })
  }
  return await new Promise((resolve, reject) => {
    child.on('error', reject)
    child.on('exit', (code, signal) => {
      exiting = true
      resolve(code ?? (signal ? 1 : 0))
    })
  })
}

async function startBackground(binary, passthroughArgs) {
  const existing = readDaemonState()
  if (existing && isPidRunning(existing.pid)) {
    throw new Error(`standalone agent already running (pid ${existing.pid}) at ${existing.url}`)
  }
  const host = bindHost()
  const port = mcpPort()
  const url = mcpUrl(host, port)
  const health = healthUrl(host, port)
  fs.mkdirSync(runtimeDir(), { recursive: true, mode: 0o700 })
  const logPath = path.join(runtimeDir(), 'daemon.log')
  const logFd = fs.openSync(logPath, 'a')
  const child = spawn(binary, ['--mode=standalone', '--transport=http', ...passthroughArgs], {
    detached: true,
    stdio: ['ignore', logFd, logFd],
    env: buildAgentEnv(),
  })
  let spawnError = null
  let childExit = null
  child.once('error', error => { spawnError = error })
  child.once('exit', (code, signal) => { childExit = { code, signal } })
  child.unref()
  fs.closeSync(logFd)
  writeDaemonState({
    pid: child.pid,
    host,
    port,
    url,
    health,
    startedAt: new Date().toISOString(),
    logPath,
  })
  try {
    await waitForHealth(health, HEALTH_WAIT_MS, () => {
      if (spawnError) return `child process failed: ${spawnError.message}`
      if (childExit) return `child process exited before health (code=${childExit.code}, signal=${childExit.signal || 'none'})`
      return null
    })
  } catch (error) {
    try { process.kill(child.pid, 'SIGTERM') } catch { /* ignore */ }
    clearDaemonState()
    throw new Error(`failed to start standalone agent: ${error.message}`)
  }
  console.log(url)
  return 0
}

async function cmdStop() {
  const state = readDaemonState()
  if (!state || !isPidRunning(state.pid)) {
    clearDaemonState()
    console.error('standalone agent is not running')
    return 0
  }
  process.kill(state.pid, 'SIGTERM')
  const deadline = Date.now() + 10_000
  while (Date.now() < deadline && isPidRunning(state.pid)) {
    await new Promise(resolve => setTimeout(resolve, 100))
  }
  if (isPidRunning(state.pid)) {
    process.kill(state.pid, 'SIGKILL')
  }
  clearDaemonState()
  console.error(`stopped standalone agent (pid ${state.pid})`)
  return 0
}

async function cmdStatus() {
  const state = readDaemonState()
  const host = state?.host || bindHost()
  const port = state?.port || mcpPort()
  const url = state?.url || mcpUrl(host, port)
  const health = state?.health || healthUrl(host, port)
  const running = state ? isPidRunning(state.pid) : false
  const healthy = await probeHealth(health)
  console.log(JSON.stringify({
    running,
    healthy,
    pid: running ? state.pid : null,
    url,
    health,
  }))
  return running && healthy ? 0 : 1
}

async function cmdUrl() {
  const state = readDaemonState()
  console.log(state?.url || mcpUrl())
  return 0
}

async function main() {
  const argv = process.argv.slice(2)
  // Backward-compatible: bare invocation with no args starts foreground (tests + local use).
  const command = argv[0] && !argv[0].startsWith('-') ? argv[0] : 'start'
  const rest = command === argv[0] ? argv.slice(1) : argv

  if (command === 'help' || command === '--help' || command === '-h') {
    printUsage()
    return 0
  }
  if (command === 'stop') return cmdStop()
  if (command === 'status') return cmdStatus()
  if (command === 'url') return cmdUrl()
  if (command !== 'start') {
    printUsage()
    throw new Error(`unknown command: ${command}`)
  }

  const background = rest.includes('--background') || rest.includes('-d')
  const passthroughArgs = rest.filter(arg => arg !== '--background' && arg !== '-d')
  const binary = await resolveBinary()
  if (background) return startBackground(binary, passthroughArgs)
  return startForeground(binary, passthroughArgs)
}

main().then(code => {
  if (typeof code === 'number') process.exitCode = code
}).catch(error => {
  console.error(error.message)
  process.exitCode = 1
})
