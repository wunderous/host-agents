const assert = require('node:assert/strict')
const crypto = require('node:crypto')
const fs = require('node:fs')
const http = require('node:http')
const net = require('node:net')
const os = require('node:os')
const path = require('node:path')
const { execFile } = require('node:child_process')
const { promisify } = require('node:util')
const zlib = require('node:zlib')
const { test, afterEach } = require('node:test')

const execFileAsync = promisify(execFile)
const packageVersion = require('./package.json').version
const indexPath = path.join(__dirname, 'index.js')
const servers = []

async function freePort() {
  const server = net.createServer()
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  const port = server.address().port
  await new Promise(resolve => server.close(resolve))
  return port
}

async function waitForClosed(url, timeoutMs = 5000) {
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    const open = await new Promise(resolve => {
      const request = http.get(url, response => {
        response.resume()
        response.on('end', () => resolve(true))
      })
      request.on('error', () => resolve(false))
      request.setTimeout(250, () => { request.destroy(); resolve(false) })
    })
    if (!open) return
    await new Promise(resolve => setTimeout(resolve, 100))
  }
  throw new Error(`listener did not close: ${url}`)
}

afterEach(() => {
  for (const server of servers.splice(0)) server.close()
})

test('downloads, verifies, caches, and re-verifies the release binary', { skip: process.platform !== 'linux' }, async () => {
  const binary = Buffer.from('#!/bin/sh\nexit 0\n')
  const archive = zlib.gzipSync(binary)
  const checksum = crypto.createHash('sha256').update(archive).digest('hex')
  let archiveRequests = 0
  const server = http.createServer((request, response) => {
    if (request.url.endsWith('/SHA256SUMS')) {
      response.end(`${checksum}  host-agent-linux-x64.gz\n`)
      return
    }
    if (request.url.endsWith('/host-agent-linux-x64.gz')) {
      archiveRequests++
      response.end(archive)
      return
    }
    response.writeHead(404)
    response.end()
  })
  servers.push(server)
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  const address = server.address()
  const cacheDir = fs.mkdtempSync(path.join(os.tmpdir(), 'opute-launcher-'))
  const env = {
    ...process.env,
    OPUTE_HOST_AGENT_CACHE_DIR: cacheDir,
    OPUTE_HOST_AGENT_RELEASE_BASE_URL: `http://127.0.0.1:${address.port}`,
    OPUTE_AGENT_MODE: '',
    OPUTE_TRANSPORT: '',
  }

  await execFileAsync(process.execPath, [indexPath, 'start'], { env, timeout: 10_000 })
  assert.equal(archiveRequests, 1)
  const cached = path.join(cacheDir, packageVersion, 'host-agent-linux-x64')
  fs.appendFileSync(cached, 'corruption')
  await execFileAsync(process.execPath, [indexPath, 'start'], { env, timeout: 10_000 })
  assert.equal(archiveRequests, 2)

  await assert.rejects(
    execFileAsync(process.execPath, [indexPath, 'start'], {
      env: {
        ...env,
        OPUTE_HOST_AGENT_CACHE_DIR: fs.mkdtempSync(path.join(os.tmpdir(), 'opute-bad-checksum-')),
        OPUTE_HOST_AGENT_SHA256: '0'.repeat(64),
      },
      timeout: 10_000,
    }),
    error => error.stderr.includes('checksum mismatch')
  )
})

test('missing local binary fails closed before a listener is claimed', async () => {
  await assert.rejects(
    execFileAsync(process.execPath, [indexPath, 'start'], {
      env: {
        ...process.env,
        OPUTE_HOST_AGENT_BINARY: path.join(os.tmpdir(), 'opute-missing-host-agent-binary'),
      },
      timeout: 10_000,
    }),
    error => /ENOENT|not found/i.test(error.stderr)
  )
})

test('url prints the default Streamable HTTP endpoint', async () => {
  const { stdout } = await execFileAsync(process.execPath, [indexPath, 'url'], {
    env: {
      ...process.env,
      HOST_MCP_PORT: '3014',
      HOST_MCP_BIND_HOST: '127.0.0.1',
      OPUTE_LOCAL_HOST_AGENT_RUNTIME_DIR: fs.mkdtempSync(path.join(os.tmpdir(), 'opute-url-')),
    },
    timeout: 10_000,
  })
  assert.equal(stdout.trim(), 'http://127.0.0.1:3014/mcp')
})

test('background daemon start/status/stop owns its listener and strips platform credentials', { skip: process.platform !== 'linux' }, async () => {
  const port = await freePort()
  const runtimeDir = fs.mkdtempSync(path.join(os.tmpdir(), 'opute-daemon-runtime-'))
  const fakeDir = fs.mkdtempSync(path.join(os.tmpdir(), 'opute-daemon-binary-'))
  const fakeBinary = path.join(fakeDir, 'fake-host-agent.js')
  const envReport = path.join(runtimeDir, 'env.json')
  fs.writeFileSync(fakeBinary, `#!/usr/bin/env node
const fs = require('node:fs')
const http = require('node:http')
fs.writeFileSync(process.env.FAKE_ENV_REPORT, JSON.stringify({
  mcpUrl: process.env.OPUTE_MCP_URL || null,
  mcpAuth: process.env.MCP_AUTH_TOKEN || null,
  bridgeToken: process.env.BRIDGE_TOKEN || null,
  reverseTunnel: process.env.OPUTE_REVERSE_TUNNEL || null,
  mode: process.env.OPUTE_AGENT_MODE,
  transport: process.env.OPUTE_TRANSPORT,
}, null, 2))
const server = http.createServer((request, response) => {
  if (request.url === '/health') { response.end(JSON.stringify({ ok: true })); return }
  response.end('{}')
})
server.listen(Number(process.env.HOST_MCP_PORT), process.env.HOST_MCP_BIND_HOST)
const stop = () => server.close(() => process.exit(0))
process.on('SIGTERM', stop)
process.on('SIGINT', stop)
`, { mode: 0o700 })

  const env = {
    ...process.env,
    OPUTE_HOST_AGENT_BINARY: fakeBinary,
    OPUTE_LOCAL_HOST_AGENT_RUNTIME_DIR: runtimeDir,
    OPUTE_STANDALONE_STATE_DIR: path.join(runtimeDir, 'state'),
    HOST_MCP_PORT: String(port),
    HOST_MCP_BIND_HOST: '127.0.0.1',
    FAKE_ENV_REPORT: envReport,
    OPUTE_MCP_URL: 'https://platform.example/mcp',
    OPUTE_REVERSE_TUNNEL: 'true',
    MCP_AUTH_TOKEN: 'platform-secret',
    BRIDGE_TOKEN: 'bridge-secret',
  }
  const started = await execFileAsync(process.execPath, [indexPath, 'start', '--background'], { env, timeout: 20_000 })
  assert.equal(started.stdout.trim(), `http://127.0.0.1:${port}/mcp`)
  const status = await execFileAsync(process.execPath, [indexPath, 'status'], { env, timeout: 10_000 })
  const statusJson = JSON.parse(status.stdout)
  assert.equal(statusJson.running, true)
  assert.equal(statusJson.healthy, true)
  const childEnv = JSON.parse(fs.readFileSync(envReport, 'utf8'))
  assert.deepEqual(childEnv, {
    mcpUrl: null,
    mcpAuth: null,
    bridgeToken: null,
    reverseTunnel: null,
    mode: 'standalone',
    transport: 'http',
  })

  await execFileAsync(process.execPath, [indexPath, 'stop'], { env, timeout: 15_000 })
  await waitForClosed(`http://127.0.0.1:${port}/health`)
  assert.equal(fs.existsSync(path.join(runtimeDir, 'daemon.json')), false)
})

test('native Windows fails before attempting an artifact download', { skip: process.platform !== 'win32' }, async () => {
  await assert.rejects(
    execFileAsync(process.execPath, [indexPath, 'start'], {
      env: { ...process.env, OPUTE_HOST_AGENT_RELEASE_BASE_URL: 'http://127.0.0.1:1' },
      timeout: 10_000,
    }),
    error => error.stderr.includes('native Windows is not supported')
  )
})

test('the packed npm tarball launches the verified binary', { skip: process.platform !== 'linux' }, async () => {
  const packageDir = fs.mkdtempSync(path.join(os.tmpdir(), 'opute-packed-'))
  await execFileAsync('npm', ['pack', '--pack-destination', packageDir], { cwd: __dirname, timeout: 30_000 })
  const tarball = path.join(packageDir, `opute-local-host-agent-${packageVersion}.tgz`)
  const installDir = fs.mkdtempSync(path.join(os.tmpdir(), 'opute-packed-install-'))
  await execFileAsync('npm', ['install', '--ignore-scripts', '--prefix', installDir, tarball], { timeout: 30_000 })
  const entry = path.join(installDir, 'node_modules', '@opute', 'local-host-agent', 'index.js')
  const binary = Buffer.from('#!/bin/sh\nexit 0\n')
  const archive = zlib.gzipSync(binary)
  const checksum = crypto.createHash('sha256').update(archive).digest('hex')
  let requests = 0
  const server = http.createServer((request, response) => {
    if (request.url.endsWith('/host-agent-linux-x64.gz')) {
      requests++
      response.end(archive)
      return
    }
    response.writeHead(404)
    response.end()
  })
  servers.push(server)
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  const address = server.address()
  await execFileAsync(process.execPath, [entry, 'start'], {
    env: {
      ...process.env,
      OPUTE_HOST_AGENT_CACHE_DIR: fs.mkdtempSync(path.join(os.tmpdir(), 'opute-packed-cache-')),
      OPUTE_HOST_AGENT_RELEASE_BASE_URL: `http://127.0.0.1:${address.port}`,
      OPUTE_HOST_AGENT_SHA256: checksum,
    },
    timeout: 10_000,
  })
  assert.equal(requests, 1)
})
