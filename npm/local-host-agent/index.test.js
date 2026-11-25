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
const launcherTestEnv = Object.fromEntries(
  Object.entries(process.env).filter(([key]) => !key.startsWith('OPUTE_') && key !== 'MCP_AUTH_TOKEN')
)
launcherTestEnv.OPUTE_REMOTE_AGENT_ID = 'npm-launcher-test-' + crypto.randomUUID()

// The workload slice the agent renders (opute-host-resource-policy.v2); the
// launcher writes this exact text (D56).
const WORKLOAD_SLICE_UNIT = '[Unit]\nDescription=Opute killable workload slice (opute-host-resource-policy.v2)\n\n[Slice]\nMemoryHigh=10G\nMemoryMax=11G\nMemorySwapMax=1G\nCPUQuota=600%\nCPUWeight=100\nTasksMax=4096\n'

// A fake `systemctl` first on PATH and a private XDG_CONFIG_HOME. It logs
// each call, answers `--user cat opute-workload.slice` from the config dir
// (or `elsewhere`), and fails every call when there is no user manager.
function fakeSystemd({ userManager = true, elsewhere = false } = {}) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'opute-fake-systemd-'))
  const bin = path.join(dir, 'bin')
  const config = path.join(dir, 'config')
  const log = path.join(dir, 'systemctl.log')
  fs.mkdirSync(bin)
  fs.writeFileSync(path.join(bin, 'systemctl'), `#!/bin/sh
echo "$*" >> "${log}"
[ "${userManager ? 1 : 0}" = 1 ] || exit 1
case "$*" in
  "--user cat opute-workload.slice")
    [ "${elsewhere ? 1 : 0}" = 1 ] && exit 0
    [ -f "$XDG_CONFIG_HOME/systemd/user/opute-workload.slice" ] ;;
  "--user show-environment"|"--user daemon-reload") exit 0 ;;
  *) exit 1 ;;
esac
`, { mode: 0o755 })
  return {
    env: { PATH: `${bin}${path.delimiter}${process.env.PATH}`, XDG_CONFIG_HOME: config },
    slice: path.join(config, 'systemd', 'user', 'opute-workload.slice'),
    calls: () => (fs.existsSync(log) ? fs.readFileSync(log, 'utf8').trim().split('\n').filter(Boolean) : []),
  }
}

// A stand-in agent for foreground starts: records that it ran, then exits.
function markerBinary() {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'opute-marker-agent-'))
  const marker = path.join(dir, 'ran')
  const binary = path.join(dir, 'agent')
  fs.writeFileSync(binary, `#!/bin/sh\necho "$*" > "${marker}"\nexit 0\n`, { mode: 0o755 })
  return { binary, ran: () => fs.existsSync(marker) }
}

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
    ...launcherTestEnv,
    OPUTE_HOST_AGENT_CACHE_DIR: cacheDir,
    OPUTE_HOST_AGENT_RELEASE_BASE_URL: `http://127.0.0.1:${address.port}`,
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

test('start requires an explicit canonical Host Agent identity before resolving a binary', async () => {
  const runtimeDir = fs.mkdtempSync(path.join(os.tmpdir(), 'opute-missing-agent-id-'))
  try {
    await assert.rejects(
      execFileAsync(process.execPath, [indexPath, 'start', '--background'], {
        env: {
          ...launcherTestEnv,
          OPUTE_REMOTE_AGENT_ID: '',
          OPUTE_HOST_AGENT_BINARY: path.join(runtimeDir, 'missing-binary'),
          OPUTE_LOCAL_HOST_AGENT_RUNTIME_DIR: path.join(runtimeDir, 'runtime'),
        },
        timeout: 10_000,
      }),
      error => error.stderr.includes('OPUTE_REMOTE_AGENT_ID is required')
    )
    assert.equal(fs.existsSync(path.join(runtimeDir, 'runtime', 'daemon.json')), false)
  } finally {
    fs.rmSync(runtimeDir, { recursive: true, force: true })
  }
})

test('missing local binary fails closed before a listener is claimed', async () => {
  await assert.rejects(
    execFileAsync(process.execPath, [indexPath, 'start'], {
      env: {
        ...launcherTestEnv,
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
      ...launcherTestEnv,
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
  agentId: process.env.OPUTE_REMOTE_AGENT_ID || null,
  reverseTunnel: process.env.OPUTE_REVERSE_TUNNEL || null,
  leftover: ['OPUTE_AGENT_MODE', 'OPUTE_HOST_ALLOW_MUTATIONS', 'OPUTE_STANDALONE_ALLOW_MUTATIONS']
    .filter(key => key in process.env),
  args: process.argv.slice(2),
  transport: process.env.OPUTE_TRANSPORT || null,
  localInstanceId: process.env.OPUTE_LOCAL_HOST_AGENT_INSTANCE_ID || null,
}, null, 2))
const server = http.createServer((request, response) => {
  if (request.url === '/health') {
    response.end(JSON.stringify({ ok: true, localInstanceId: process.env.OPUTE_LOCAL_HOST_AGENT_INSTANCE_ID || null }))
    return
  }
  response.end('{}')
})
server.listen(Number(process.env.HOST_MCP_PORT), process.env.HOST_MCP_BIND_HOST)
const stop = () => server.close(() => process.exit(0))
process.on('SIGTERM', stop)
process.on('SIGINT', stop)
`, { mode: 0o700 })

  const env = {
    ...launcherTestEnv,
    OPUTE_HOST_AGENT_BINARY: fakeBinary,
    OPUTE_LOCAL_HOST_AGENT_RUNTIME_DIR: runtimeDir,
    OPUTE_STANDALONE_STATE_DIR: path.join(runtimeDir, 'state'),
    HOST_MCP_PORT: String(port),
    HOST_MCP_BIND_HOST: '127.0.0.1',
    FAKE_ENV_REPORT: envReport,
    OPUTE_MCP_URL: 'https://platform.example/mcp',
    OPUTE_REVERSE_TUNNEL: 'true',
    OPUTE_REMOTE_AGENT_ID: 'test-local-agent-identity',
    MCP_AUTH_TOKEN: 'platform-secret',
    // Settings of earlier releases: stripped, so they grant nothing.
    OPUTE_AGENT_MODE: 'standalone',
    OPUTE_HOST_ALLOW_MUTATIONS: 'true',
    OPUTE_STANDALONE_ALLOW_MUTATIONS: 'true',
    ...fakeSystemd().env,
  }
  const started = await execFileAsync(process.execPath, [indexPath, 'start', '--background', '--allow-mutations'], { env, timeout: 20_000 })
  assert.equal(started.stdout.trim(), `http://127.0.0.1:${port}/mcp`)
  const daemonStatePath = path.join(runtimeDir, 'daemon.json')
  const firstState = JSON.parse(fs.readFileSync(daemonStatePath, 'utf8'))
  const startedAgain = await execFileAsync(process.execPath, [indexPath, 'start', '--background'], { env, timeout: 20_000 })
  assert.equal(startedAgain.stdout.trim(), started.stdout.trim())
  const secondState = JSON.parse(fs.readFileSync(daemonStatePath, 'utf8'))
  assert.equal(secondState.pid, firstState.pid)
  assert.equal(typeof secondState.instanceId, 'string')
  const status = await execFileAsync(process.execPath, [indexPath, 'status'], { env, timeout: 10_000 })
  const statusJson = JSON.parse(status.stdout)
  assert.equal(statusJson.running, true)
  assert.equal(statusJson.healthy, true)
  const childEnv = JSON.parse(fs.readFileSync(envReport, 'utf8'))
  assert.deepEqual(childEnv, {
    mcpUrl: null,
    mcpAuth: 'platform-secret',
    agentId: 'test-local-agent-identity',
    reverseTunnel: null,
    leftover: [],
    args: ['--transport=http', '--allow-mutations'],
    transport: null,
    localInstanceId: secondState.instanceId,
  })

  await execFileAsync(process.execPath, [indexPath, 'stop'], { env, timeout: 15_000 })
  await waitForClosed(`http://127.0.0.1:${port}/health`)
  assert.equal(fs.existsSync(path.join(runtimeDir, 'daemon.json')), false)
})

test('start --allow-mutations creates the workload slice before the agent starts', { skip: process.platform !== 'linux' }, async () => {
  const systemd = fakeSystemd()
  const agent = markerBinary()
  const env = { ...launcherTestEnv, ...systemd.env, OPUTE_HOST_AGENT_BINARY: agent.binary }
  const started = await execFileAsync(process.execPath, [indexPath, 'start', '--allow-mutations'], { env, timeout: 20_000 })
  assert.equal(fs.readFileSync(systemd.slice, 'utf8'), WORKLOAD_SLICE_UNIT)
  assert.equal(fs.statSync(systemd.slice).mode & 0o777, 0o644)
  assert.deepEqual(systemd.calls(), [
    '--user cat opute-workload.slice',
    '--user show-environment',
    '--user daemon-reload',
    '--user cat opute-workload.slice',
  ])
  assert.match(started.stderr, new RegExp(`created ${systemd.slice.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}`))
  assert.equal(started.stdout, '')
  assert.equal(agent.ran(), true)
})

test('start --allow-mutations keeps an existing workload slice', { skip: process.platform !== 'linux' }, async () => {
  for (const where of ['config', 'elsewhere']) {
    const systemd = fakeSystemd({ elsewhere: where === 'elsewhere' })
    const stricter = '[Slice]\nMemoryMax=2G\n'
    if (where === 'config') {
      fs.mkdirSync(path.dirname(systemd.slice), { recursive: true })
      fs.writeFileSync(systemd.slice, stricter)
    }
    const agent = markerBinary()
    const env = { ...launcherTestEnv, ...systemd.env, OPUTE_HOST_AGENT_BINARY: agent.binary }
    await execFileAsync(process.execPath, [indexPath, 'start', '--allow-mutations'], { env, timeout: 20_000 })
    assert.deepEqual(systemd.calls(), ['--user cat opute-workload.slice'], where)
    assert.equal(fs.existsSync(systemd.slice) ? fs.readFileSync(systemd.slice, 'utf8') : null, where === 'config' ? stricter : null, where)
    assert.equal(agent.ran(), true, where)
  }
})

test('start --allow-mutations without a systemd user session refuses before the agent starts', { skip: process.platform !== 'linux' }, async () => {
  const systemd = fakeSystemd({ userManager: false })
  const agent = markerBinary()
  const env = { ...launcherTestEnv, ...systemd.env, OPUTE_HOST_AGENT_BINARY: agent.binary }
  await assert.rejects(
    execFileAsync(process.execPath, [indexPath, 'start', '--background', '--allow-mutations'], { env, timeout: 20_000 }),
    error => {
      assert.equal(error.code, 1)
      assert.match(error.stderr, /systemd user session/)
      assert.match(error.stderr, /opute-workload\.slice/)
      assert.match(error.stderr, /MemoryMax=11G/)
      return true
    },
  )
  assert.equal(fs.existsSync(systemd.slice), false)
  assert.equal(agent.ran(), false)
})

test('start without --allow-mutations touches no slice', { skip: process.platform !== 'linux' }, async () => {
  const systemd = fakeSystemd()
  const agent = markerBinary()
  const env = { ...launcherTestEnv, ...systemd.env, OPUTE_HOST_AGENT_BINARY: agent.binary }
  await execFileAsync(process.execPath, [indexPath, 'start'], { env, timeout: 20_000 })
  assert.deepEqual(systemd.calls(), [])
  assert.equal(fs.existsSync(systemd.slice), false)
  assert.equal(agent.ran(), true)
})

test('background start refuses a foreign listener without overwriting daemon state', { skip: process.platform !== 'linux' }, async () => {
  const port = await freePort()
  const runtimeDir = fs.mkdtempSync(path.join(os.tmpdir(), 'opute-foreign-runtime-'))
  const foreign = http.createServer((request, response) => {
    if (request.url === '/health') response.end(JSON.stringify({ ok: true }))
    else response.end('foreign')
  })
  servers.push(foreign)
  await new Promise(resolve => foreign.listen(port, '127.0.0.1', resolve))
  await assert.rejects(
    execFileAsync(process.execPath, [indexPath, 'start', '--background'], {
      env: {
        ...launcherTestEnv,
        OPUTE_HOST_AGENT_BINARY: path.join(runtimeDir, 'unused-binary'),
        HOST_MCP_PORT: String(port),
        OPUTE_LOCAL_HOST_AGENT_RUNTIME_DIR: runtimeDir,
      },
      timeout: 10_000,
    }),
    error => error.stderr.includes(`port ${port} already in use`)
  )
  assert.equal(fs.existsSync(path.join(runtimeDir, 'daemon.json')), false)
})

test('background start fails closed for a live reused PID', { skip: process.platform !== 'linux' }, async () => {
  const port = await freePort()
  const runtimeDir = fs.mkdtempSync(path.join(os.tmpdir(), 'opute-reused-pid-runtime-'))
  fs.writeFileSync(path.join(runtimeDir, 'daemon.json'), JSON.stringify({
    pid: process.pid,
    host: '127.0.0.1',
    port,
    instanceId: 'not-owned',
  }))
  await assert.rejects(
    execFileAsync(process.execPath, [indexPath, 'start', '--background'], {
      env: {
        ...launcherTestEnv,
        OPUTE_HOST_AGENT_BINARY: path.join(runtimeDir, 'unused-binary'),
        HOST_MCP_PORT: String(port),
        OPUTE_LOCAL_HOST_AGENT_RUNTIME_DIR: runtimeDir,
      },
      timeout: 10_000,
    }),
    error => error.stderr.includes('refusing to kill it')
  )
  assert.doesNotThrow(() => process.kill(process.pid, 0))
  assert.equal(fs.existsSync(path.join(runtimeDir, 'daemon.json')), true)
})

test('background start clears a dead pid when the port is free', { skip: process.platform !== 'linux' }, async () => {
  const port = await freePort()
  const runtimeDir = fs.mkdtempSync(path.join(os.tmpdir(), 'opute-stale-pid-runtime-'))
  const fakeDir = fs.mkdtempSync(path.join(os.tmpdir(), 'opute-stale-pid-binary-'))
  const fakeBinary = path.join(fakeDir, 'fake-host-agent.js')
  fs.writeFileSync(fakeBinary, `#!/usr/bin/env node
const http = require('node:http')
const server = http.createServer((request, response) => {
  if (request.url === '/health') {
    response.end(JSON.stringify({ ok: true, localInstanceId: process.env.OPUTE_LOCAL_HOST_AGENT_INSTANCE_ID || null }))
    return
  }
  response.end('{}')
})
server.listen(Number(process.env.HOST_MCP_PORT), process.env.HOST_MCP_BIND_HOST)
const stop = () => server.close(() => process.exit(0))
process.on('SIGTERM', stop)
process.on('SIGINT', stop)
`, { mode: 0o700 })
  fs.writeFileSync(path.join(runtimeDir, 'daemon.json'), JSON.stringify({
    pid: 2_147_483_646,
    host: '127.0.0.1',
    port,
    instanceId: 'dead-stale',
  }))
  const env = {
    ...launcherTestEnv,
    OPUTE_HOST_AGENT_BINARY: fakeBinary,
    OPUTE_LOCAL_HOST_AGENT_RUNTIME_DIR: runtimeDir,
    OPUTE_STANDALONE_STATE_DIR: path.join(runtimeDir, 'state'),
    HOST_MCP_PORT: String(port),
    HOST_MCP_BIND_HOST: '127.0.0.1',
  }
  const started = await execFileAsync(process.execPath, [indexPath, 'start', '--background'], { env, timeout: 20_000 })
  assert.equal(started.stdout.trim(), `http://127.0.0.1:${port}/mcp`)
  const state = JSON.parse(fs.readFileSync(path.join(runtimeDir, 'daemon.json'), 'utf8'))
  assert.notEqual(state.pid, 2_147_483_646)
  assert.equal(typeof state.instanceId, 'string')
  assert.notEqual(state.instanceId, 'dead-stale')
  await execFileAsync(process.execPath, [indexPath, 'stop'], { env, timeout: 15_000 })
  await waitForClosed(`http://127.0.0.1:${port}/health`)
})

test('background start refuses a dead pid when a foreign listener holds the port', { skip: process.platform !== 'linux' }, async () => {
  const port = await freePort()
  const runtimeDir = fs.mkdtempSync(path.join(os.tmpdir(), 'opute-stale-foreign-runtime-'))
  fs.writeFileSync(path.join(runtimeDir, 'daemon.json'), JSON.stringify({
    pid: 2_147_483_645,
    host: '127.0.0.1',
    port,
    instanceId: 'dead-stale',
  }))
  const foreign = http.createServer((request, response) => {
    if (request.url === '/health') response.end(JSON.stringify({ ok: true }))
    else response.end('foreign')
  })
  servers.push(foreign)
  await new Promise(resolve => foreign.listen(port, '127.0.0.1', resolve))
  await assert.rejects(
    execFileAsync(process.execPath, [indexPath, 'start', '--background'], {
      env: {
        ...launcherTestEnv,
        OPUTE_HOST_AGENT_BINARY: path.join(runtimeDir, 'unused-binary'),
        HOST_MCP_PORT: String(port),
        OPUTE_LOCAL_HOST_AGENT_RUNTIME_DIR: runtimeDir,
      },
      timeout: 10_000,
    }),
    error => error.stderr.includes(`port ${port} already in use`)
  )
  assert.equal(fs.existsSync(path.join(runtimeDir, 'daemon.json')), false)
})

test('stop refuses a live reused PID without ownership', { skip: process.platform !== 'linux' }, async () => {
  const port = await freePort()
  const runtimeDir = fs.mkdtempSync(path.join(os.tmpdir(), 'opute-stop-reused-runtime-'))
  fs.writeFileSync(path.join(runtimeDir, 'daemon.json'), JSON.stringify({
    pid: process.pid,
    host: '127.0.0.1',
    port,
    instanceId: 'not-owned',
  }))
  await assert.rejects(
    execFileAsync(process.execPath, [indexPath, 'stop'], {
      env: { ...launcherTestEnv, HOST_MCP_PORT: String(port), OPUTE_LOCAL_HOST_AGENT_RUNTIME_DIR: runtimeDir },
      timeout: 10_000,
    }),
    error => error.stderr.includes('refusing to kill it')
  )
  assert.doesNotThrow(() => process.kill(process.pid, 0))
  assert.equal(fs.existsSync(path.join(runtimeDir, 'daemon.json')), true)
})

test('background start uses stored owned endpoint when daemon state points at another port', { skip: process.platform !== 'linux' }, async () => {
  const ownedPort = await freePort()
  const requestedPort = await freePort()
  const runtimeDir = fs.mkdtempSync(path.join(os.tmpdir(), 'opute-state-port-runtime-'))
  const fakeDir = fs.mkdtempSync(path.join(os.tmpdir(), 'opute-state-port-binary-'))
  const fakeBinary = path.join(fakeDir, 'fake-host-agent.js')
  fs.writeFileSync(fakeBinary, `#!/usr/bin/env node
const http = require('node:http')
const server = http.createServer((request, response) => {
  if (request.url === '/health') {
    response.end(JSON.stringify({ ok: true, localInstanceId: process.env.OPUTE_LOCAL_HOST_AGENT_INSTANCE_ID || null }))
    return
  }
  response.end('{}')
})
server.listen(Number(process.env.HOST_MCP_PORT), process.env.HOST_MCP_BIND_HOST)
const stop = () => server.close(() => process.exit(0))
process.on('SIGTERM', stop)
process.on('SIGINT', stop)
`, { mode: 0o700 })
  const env = {
    ...launcherTestEnv,
    OPUTE_HOST_AGENT_BINARY: fakeBinary,
    OPUTE_LOCAL_HOST_AGENT_RUNTIME_DIR: runtimeDir,
    OPUTE_STANDALONE_STATE_DIR: path.join(runtimeDir, 'state'),
    HOST_MCP_PORT: String(ownedPort),
    HOST_MCP_BIND_HOST: '127.0.0.1',
  }
  const started = await execFileAsync(process.execPath, [indexPath, 'start', '--background'], { env, timeout: 20_000 })
  assert.equal(started.stdout.trim(), `http://127.0.0.1:${ownedPort}/mcp`)
  const firstState = JSON.parse(fs.readFileSync(path.join(runtimeDir, 'daemon.json'), 'utf8'))
  const again = await execFileAsync(process.execPath, [indexPath, 'start', '--background'], {
    env: { ...env, HOST_MCP_PORT: String(requestedPort) },
    timeout: 20_000,
  })
  assert.equal(again.stdout.trim(), `http://127.0.0.1:${ownedPort}/mcp`)
  const secondState = JSON.parse(fs.readFileSync(path.join(runtimeDir, 'daemon.json'), 'utf8'))
  assert.equal(secondState.pid, firstState.pid)
  assert.equal(secondState.port, ownedPort)
  await execFileAsync(process.execPath, [indexPath, 'stop'], { env, timeout: 15_000 })
  await waitForClosed(`http://127.0.0.1:${ownedPort}/health`)
})

test('background start fails closed when a foreign listener claims the port after preflight', { skip: process.platform !== 'linux' }, async () => {
  const port = await freePort()
  const runtimeDir = fs.mkdtempSync(path.join(os.tmpdir(), 'opute-port-race-runtime-'))
  const fakeDir = fs.mkdtempSync(path.join(os.tmpdir(), 'opute-port-race-binary-'))
  const fakeBinary = path.join(fakeDir, 'fake-host-agent.js')
  const raceMarker = path.join(runtimeDir, 'race-ready')
  fs.writeFileSync(fakeBinary, `#!/usr/bin/env node
const fs = require('node:fs')
const http = require('node:http')
const net = require('node:net')
const marker = process.env.RACE_MARKER
fs.writeFileSync(marker, 'ready')
const deadline = Date.now() + 5000
const wait = () => {
  const probe = net.createConnection({ host: process.env.HOST_MCP_BIND_HOST, port: Number(process.env.HOST_MCP_PORT) })
  probe.once('connect', () => {
    probe.destroy()
    process.exit(2)
  })
  probe.once('error', () => {
    if (Date.now() > deadline) process.exit(3)
    setTimeout(wait, 50)
  })
}
wait()
`, { mode: 0o700 })
  const env = {
    ...launcherTestEnv,
    OPUTE_HOST_AGENT_BINARY: fakeBinary,
    OPUTE_LOCAL_HOST_AGENT_RUNTIME_DIR: runtimeDir,
    OPUTE_STANDALONE_STATE_DIR: path.join(runtimeDir, 'state'),
    HOST_MCP_PORT: String(port),
    HOST_MCP_BIND_HOST: '127.0.0.1',
    RACE_MARKER: raceMarker,
  }
  const startPromise = execFileAsync(process.execPath, [indexPath, 'start', '--background'], { env, timeout: 20_000 })
  const raceDeadline = Date.now() + 5000
  while (!fs.existsSync(raceMarker) && Date.now() < raceDeadline) {
    await new Promise(resolve => setTimeout(resolve, 20))
  }
  const foreign = http.createServer((_request, response) => {
    response.end(JSON.stringify({ ok: true }))
  })
  servers.push(foreign)
  await new Promise((resolve, reject) => {
    foreign.once('error', reject)
    foreign.listen(port, '127.0.0.1', resolve)
  })
  await assert.rejects(
    startPromise,
    error => /failed to start standalone agent|port .+ already in use|child process exited/.test(error.stderr)
  )
  assert.equal(await new Promise(resolve => {
    const req = http.get(`http://127.0.0.1:${port}/health`, response => {
      response.resume()
      response.on('end', () => resolve(response.statusCode === 200))
    })
    req.on('error', () => resolve(false))
  }), true)
})

test('native Windows fails before attempting an artifact download', { skip: process.platform !== 'win32' }, async () => {
  await assert.rejects(
    execFileAsync(process.execPath, [indexPath, 'start'], {
      env: { ...launcherTestEnv, OPUTE_HOST_AGENT_RELEASE_BASE_URL: 'http://127.0.0.1:1' },
      timeout: 10_000,
    }),
    error => error.stderr.includes('native Windows is not supported')
  )
})

test('the packed npm tarball launches the verified binary', { skip: process.platform !== 'linux' }, async () => {
  const packageDir = fs.mkdtempSync(path.join(os.tmpdir(), 'opute-packed-'))
  await execFileAsync('npm', ['pack', '--pack-destination', packageDir], { cwd: __dirname, timeout: 30_000 })
  const tarball = path.join(packageDir, `opute-host-agent-${packageVersion}.tgz`)
  const installDir = fs.mkdtempSync(path.join(os.tmpdir(), 'opute-packed-install-'))
  await execFileAsync('npm', ['install', '--ignore-scripts', '--prefix', installDir, tarball], { timeout: 30_000 })
  const entry = path.join(installDir, 'node_modules', '@opute', 'host-agent', 'index.js')
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
      ...launcherTestEnv,
      OPUTE_HOST_AGENT_CACHE_DIR: fs.mkdtempSync(path.join(os.tmpdir(), 'opute-packed-cache-')),
      OPUTE_HOST_AGENT_RELEASE_BASE_URL: `http://127.0.0.1:${address.port}`,
      OPUTE_HOST_AGENT_SHA256: checksum,
    },
    timeout: 10_000,
  })
  assert.equal(requests, 1)
})
