const assert = require('node:assert/strict')
const crypto = require('node:crypto')
const fs = require('node:fs')
const net = require('node:net')
const os = require('node:os')
const path = require('node:path')
const { execFile } = require('node:child_process')
const { promisify } = require('node:util')
const { test } = require('node:test')

const execFileAsync = promisify(execFile)
const PACKAGE = '@opute/host-agent'
const PACKAGE_VERSION = JSON.parse(fs.readFileSync(path.join(__dirname, 'package.json'), 'utf8')).version

function freePort() {
  return new Promise((resolve, reject) => {
    const server = net.createServer()
    server.once('error', reject)
    server.listen(0, '127.0.0.1', () => {
      const port = server.address().port
      server.close(error => error ? reject(error) : resolve(port))
    })
  })
}

function cleanEnv(overrides) {
  const env = {}
  for (const [key, value] of Object.entries(process.env)) {
    if (!key.startsWith('OPUTE_') && !['MCP_AUTH_TOKEN', 'NPM_TOKEN', 'NODE_AUTH_TOKEN'].includes(key)) {
      env[key] = value
    }
  }
  return { ...env, ...overrides }
}

async function runLauncher(packageSpec, cache, npmrc, env, ...args) {
  return await execFileAsync('npm', [
    'exec', '--yes', '--cache', cache, '--userconfig', npmrc,
    `--package=${packageSpec}`, '--', 'opute-host-agent', ...args,
  ], { cwd: os.tmpdir(), env, timeout: 120_000 })
}

async function waitHealth(url, timeoutMs = 20_000) {
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    try {
      const response = await fetch(url)
      if (response.ok) return await response.json()
    } catch {}
    await new Promise(resolve => setTimeout(resolve, 100))
  }
  throw new Error(`timed out waiting for ${url}`)
}

async function rpcResponse(url, state, method, params = {}) {
  const headers = {
    Accept: 'application/json, text/event-stream',
    'Content-Type': 'application/json',
    'MCP-Protocol-Version': '2026-07-28',
    'Mcp-Method': method,
    Origin: new URL(url).origin,
  }
  if (state.token) headers.Authorization = `Bearer ${state.token}`
  if (method === 'tools/call' && params.name) headers['Mcp-Name'] = params.name
  const body = { jsonrpc: '2.0', method, id: state.nextId++ }
  body.params = {
    ...params,
    _meta: {
      'io.modelcontextprotocol/protocolVersion': '2026-07-28',
      'io.modelcontextprotocol/clientCapabilities': {},
      'io.modelcontextprotocol/clientInfo': { name: 'opute-published-readonly-canary', version: '1' },
    },
  }
  return await fetch(url, { method: 'POST', headers, body: JSON.stringify(body) })
}

async function rpc(url, state, method, params = {}) {
  const response = await rpcResponse(url, state, method, params)
  assert.equal(response.status, 200, `${method} HTTP ${response.status}`)
  const text = await response.text()
  assert.ok(text.trim(), `${method} returned an empty body`)
  const envelope = JSON.parse(text)
  assert.equal(envelope.error, undefined, `${method} returned an MCP error`)
  return envelope.result || {}
}

async function callTool(url, state, name, arguments_ = {}) {
  const result = await rpc(url, state, 'tools/call', { name, arguments: arguments_ })
  assert.notEqual(result.isError, true, `${name} returned an MCP error`)
  assert.ok(result.structuredContent && typeof result.structuredContent === 'object', `${name} omitted structuredContent`)
  return result.structuredContent
}

test('published npm launcher completes authenticated read-only first success', {
  skip: process.env.RUN_PUBLISHED_READONLY_NPM_CANARY !== 'true' || process.platform !== 'linux',
}, async () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'opute-published-readonly-canary-'))
  const cache = path.join(root, 'npm-cache')
  const npmrc = path.join(root, 'npmrc')
  fs.writeFileSync(npmrc, 'registry=https://registry.npmjs.org/\n')
  const localPackage = process.env.PUBLISHED_NPM_PACKAGE
  const version = process.env.PUBLISHED_NPM_VERSION || PACKAGE_VERSION
  const packageSpec = localPackage ? path.resolve(localPackage) : `${PACKAGE}@${version}`
  if (!localPackage && !process.env.PUBLISHED_NPM_VERSION) {
    const lookup = await execFileAsync('npm', ['view', PACKAGE, 'version', '--userconfig', npmrc], {
      env: cleanEnv({}), timeout: 60_000,
    })
    assert.equal(lookup.stdout.trim(), version, `registry version does not match package candidate ${version}`)
  }

  const port = await freePort()
  const token = crypto.randomBytes(32).toString('hex')
  const agentId = crypto.randomUUID()
  const binaryOverride = process.env.OPUTE_HOST_AGENT_BINARY
  const env = cleanEnv({
    NPM_CONFIG_USERCONFIG: npmrc,
    NPM_CONFIG_CACHE: cache,
    OPUTE_HOST_AGENT_CACHE_DIR: path.join(root, 'agent-cache'),
    OPUTE_LOCAL_HOST_AGENT_RUNTIME_DIR: path.join(root, 'runtime'),
    OPUTE_STANDALONE_STATE_DIR: path.join(root, 'state'),
    OPUTE_REMOTE_AGENT_ID: agentId,
    HOST_MCP_BIND_HOST: '127.0.0.1',
    HOST_MCP_PORT: String(port),
    MCP_AUTH_TOKEN: token,
    ...(binaryOverride ? { OPUTE_HOST_AGENT_BINARY: binaryOverride } : {}),
  })
  const healthUrl = `http://127.0.0.1:${port}/health`
  const mcpUrl = `http://127.0.0.1:${port}/mcp`
  let started = false

  try {
    const start = await runLauncher(packageSpec, cache, npmrc, env, 'start', '--background')
    started = true
    assert.equal(start.stdout.trim(), mcpUrl)

    const health = await waitHealth(healthUrl)
    assert.equal(health.ok, true)
    assert.ok(health.agentId === agentId, 'launcher did not preserve OPUTE_REMOTE_AGENT_ID')
    const daemonState = JSON.parse(fs.readFileSync(path.join(env.OPUTE_LOCAL_HOST_AGENT_RUNTIME_DIR, 'daemon.json'), 'utf8'))
    assert.ok(health.localInstanceId === daemonState.instanceId, 'health did not report the launcher-owned daemon identity')

    const status = await runLauncher(packageSpec, cache, npmrc, env, 'status')
    const statusPayload = JSON.parse(status.stdout)
    assert.equal(statusPayload.running, true)
    assert.equal(statusPayload.healthy, true, 'launcher could not prove ownership of its daemon')
    assert.equal(statusPayload.url, mcpUrl)

    const unauthenticatedHealth = await fetch(healthUrl)
    assert.equal(unauthenticatedHealth.status, 200, '/health should be open for liveness checks')

    const wrongToken = await rpcResponse(mcpUrl, { nextId: 1, token: 'invalid-readonly-canary-token' }, 'server/discover')
    assert.equal(wrongToken.status, 401, 'wrong Bearer token did not receive HTTP 401')
    await wrongToken.body?.cancel()

    const state = { nextId: 2, token }
    const discovery = await rpc(mcpUrl, state, 'server/discover')
    assert.equal(discovery.resultType, 'complete', 'server/discover did not complete')

    const listed = await rpc(mcpUrl, state, 'tools/list')
    const names = new Set((Array.isArray(listed.tools) ? listed.tools : []).map(tool => tool?.name))
    assert.ok(names.size > 0, 'authenticated tools/list returned no tools')
    assert.ok(names.has('get_host_info'), 'authenticated tools/list omitted get_host_info')

    const catalog = await callTool(mcpUrl, state, 'get_capability_catalog', {})
    assert.equal(typeof catalog.catalogRevision, 'string', 'catalog revision is missing')
    assert.ok(catalog.catalogRevision.length > 0, 'catalog revision is empty')
    assert.ok(Array.isArray(catalog.tools), 'catalog descriptors are missing')
    const hostInfoTool = catalog.tools.find(tool => tool?.name === 'get_host_info')
    assert.ok(hostInfoTool, 'catalog omitted get_host_info')
    assert.equal(hostInfoTool.effect, 'read', 'get_host_info is not declared read-only')

    const host = await callTool(mcpUrl, state, 'get_host_info', {})
    assert.equal(typeof host.uri, 'string', 'get_host_info omitted uri')
    assert.equal(typeof host.hostName, 'string', 'get_host_info omitted hostName')
    assert.equal(typeof host.providerId, 'string', 'get_host_info omitted providerId')
    assert.ok(Array.isArray(host.supportedTools), 'get_host_info omitted supportedTools')
    assert.equal(JSON.stringify(host).includes(token), false, 'get_host_info echoed the Bearer token')

    const output = process.env.PUBLISHED_CANARY_EVIDENCE_OUTPUT
    if (output) {
      const sourceSha = process.env.PUBLISHED_CANARY_SOURCE_SHA
      const runId = Number(process.env.PUBLISHED_CANARY_RUN_ID)
      const runAttempt = Number(process.env.PUBLISHED_CANARY_RUN_ATTEMPT)
      assert.match(sourceSha || '', /^[0-9a-f]{40}$/, 'published evidence needs the release source SHA')
      assert.ok(Number.isSafeInteger(runId) && runId > 0, 'published evidence needs the workflow run id')
      assert.ok(Number.isSafeInteger(runAttempt) && runAttempt > 0, 'published evidence needs the workflow attempt')
      fs.writeFileSync(output, `${JSON.stringify({
        packageName: PACKAGE,
        packageVersion: version,
        catalogRevision: catalog.catalogRevision,
        sourceSha,
        runId,
        runAttempt,
        checks: {
          explicitIdentity: true,
          openHealth: true,
          invalidTokenRejected: true,
          authenticatedDiscovery: true,
          authenticatedToolsList: true,
          structuredGetHostInfo: true,
          readOnly: true,
        },
      }, null, 2)}\n`)
    }
  } catch (error) {
    const logPath = path.join(env.OPUTE_LOCAL_HOST_AGENT_RUNTIME_DIR, 'daemon.log')
    if (error && typeof error === 'object' && fs.existsSync(logPath)) {
      const safeLog = fs.readFileSync(logPath, 'utf8')
        .replaceAll(token, '[redacted]')
        .replaceAll(agentId, '[redacted]')
        .slice(-8_000)
      error.message += `\nHost Agent daemon log:\n${safeLog}`
    }
    throw error
  } finally {
    try {
      if (started) await runLauncher(packageSpec, cache, npmrc, env, 'stop')
    } finally {
      fs.rmSync(root, { recursive: true, force: true })
    }
  }
})
