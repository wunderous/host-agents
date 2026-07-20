const assert = require('node:assert/strict')
const fs = require('node:fs')
const net = require('node:net')
const os = require('node:os')
const path = require('node:path')
const { execFile } = require('node:child_process')
const { promisify } = require('node:util')
const { test } = require('node:test')

const execFileAsync = promisify(execFile)
const PACKAGE = '@opute/host-agent'

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
    if (!key.startsWith('OPUTE_') && !['MCP_AUTH_TOKEN', 'BRIDGE_TOKEN', 'NPM_TOKEN', 'NODE_AUTH_TOKEN'].includes(key)) {
      env[key] = value
    }
  }
  return { ...env, ...overrides }
}

async function runLauncher(npm, version, cache, npmrc, env, ...args) {
  return await execFileAsync(npm, [
    'exec', '--yes', '--cache', cache, '--userconfig', npmrc,
    `--package=${PACKAGE}@${version}`, '--', 'opute-host-agent', ...args,
  ], { cwd: os.tmpdir(), env, timeout: 120_000 })
}

async function waitHealth(url, timeoutMs = 20_000) {
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    try {
      const response = await fetch(url)
      if (response.ok && (await response.json()).ok === true) return
    } catch {}
    await new Promise(resolve => setTimeout(resolve, 100))
  }
  throw new Error(`timed out waiting for ${url}`)
}

async function rpc(url, state, method, params = undefined) {
  const headers = {
    Accept: 'application/json, text/event-stream',
    'Content-Type': 'application/json',
    'Mcp-Protocol-Version': state.protocolVersion,
  }
  if (state.sessionId) headers['Mcp-Session-Id'] = state.sessionId
  const body = { jsonrpc: '2.0', method }
  if (!method.startsWith('notifications/')) body.id = state.nextId++
  if (params !== undefined) body.params = params
  const response = await fetch(url, { method: 'POST', headers, body: JSON.stringify(body) })
  assert.equal(response.ok, true, `${method} HTTP ${response.status}`)
  const sessionId = response.headers.get('Mcp-Session-Id')
  if (sessionId) state.sessionId = sessionId
  const text = await response.text()
  if (!text.trim()) return {}
  const result = JSON.parse(text)
  assert.equal(result.error, undefined, `${method} returned an MCP error`)
  return result.result || {}
}

function structured(result) {
  return result.structuredContent && typeof result.structuredContent === 'object'
    ? result.structuredContent
    : {}
}

async function callTool(url, state, name, arguments_ = {}) {
  const result = await rpc(url, state, 'tools/call', { name, arguments: arguments_ })
  assert.notEqual(result.isError, true, `${name} returned an MCP error`)
  return structured(result)
}

function operationId(result) {
  const id = result.taskId || result.operationId
  assert.ok(id, `operation response has no taskId/operationId: ${JSON.stringify(result)}`)
  return String(id)
}

async function waitOperation(url, state, id, timeoutMs = 600_000) {
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    const result = await callTool(url, state, 'get_operation', { operationId: id })
    const status = String(result.status || '').toLowerCase()
    if (['completed', 'failed', 'cancelled', 'canceled'].includes(status)) {
      assert.equal(status, 'completed', `operation ${id} ended with ${status}`)
      return result
    }
    await new Promise(resolve => setTimeout(resolve, 2_000))
  }
  assert.fail(`operation ${id} timed out`)
}

async function vmNames(url, state) {
  const result = await callTool(url, state, 'list_vms', { fast: true })
  return new Set((Array.isArray(result.vms) ? result.vms : [])
    .filter(vm => vm && typeof vm === 'object')
    .map(vm => String(vm.name)))
}

test('published npm launcher black-box canary', { skip: process.env.RUN_PUBLISHED_NPM_CANARY !== 'true' || process.platform !== 'linux' }, async () => {
  const npm = 'npm'
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'opute-published-npm-canary-'))
  const cache = path.join(root, 'npm-cache')
  const npmrc = path.join(root, 'npmrc')
  fs.writeFileSync(npmrc, 'registry=https://registry.npmjs.org/\n')
  const lookup = process.env.PUBLISHED_NPM_VERSION
    ? { stdout: process.env.PUBLISHED_NPM_VERSION }
    : await execFileAsync(npm, ['view', PACKAGE, 'version', '--userconfig', npmrc], { env: cleanEnv({}), timeout: 60_000 })
  const version = lookup.stdout.trim()
  assert.ok(version, 'published npm version is empty')
  const port = await freePort()
  const env = cleanEnv({
    NPM_CONFIG_USERCONFIG: npmrc,
    NPM_CONFIG_CACHE: cache,
    OPUTE_HOST_AGENT_CACHE_DIR: path.join(root, 'agent-cache'),
    OPUTE_LOCAL_HOST_AGENT_RUNTIME_DIR: path.join(root, 'runtime'),
    OPUTE_STANDALONE_STATE_DIR: path.join(root, 'state'),
    HOST_MCP_BIND_HOST: '127.0.0.1',
    HOST_MCP_PORT: String(port),
  })
  const health = `http://127.0.0.1:${port}/health`
  const mcp = `http://127.0.0.1:${port}/mcp`
  try {
    const start = await runLauncher(npm, version, cache, npmrc, env, 'start', '--background')
    assert.equal(start.stdout.trim(), `${mcp}`)
    await waitHealth(health)
    const state = { nextId: 1, sessionId: null, protocolVersion: '2024-11-05' }
    const initialize = await rpc(mcp, state, 'initialize', {
      protocolVersion: state.protocolVersion,
      capabilities: {},
      clientInfo: { name: 'published-npm-canary', version: '1' },
    })
    assert.equal(initialize.serverInfo.name, 'host-agent')
    assert.equal(initialize.serverInfo.version, version)
    await rpc(mcp, state, 'notifications/initialized')
    const listed = await rpc(mcp, state, 'tools/list')
    const names = new Set(listed.tools.map(tool => tool.name))
    for (const name of ['check_local_prerequisites', 'get_local_status', 'list_vms', 'create_vm', 'get_operation']) {
      assert.equal(names.has(name), true, `published tools/list missing ${name}`)
    }
    for (const name of ['register_host_agent', 'host_agent_heartbeat', 'dispatch_host_operation', 'agent_shell']) {
      assert.equal(names.has(name), false, `published tools/list leaked ${name}`)
    }
    const denied = await rpc(mcp, state, 'tools/call', { name: 'create_vm', arguments: { vmName: 'opute-published-npm-denied' } })
    assert.equal(denied.isError, true, 'published default mutation policy allowed create_vm')

    await runLauncher(npm, version, cache, npmrc, env, 'stop')
    const incus = await execFileAsync('incus', ['list', '--format', 'csv'], { timeout: 30_000 })
    assert.equal(typeof incus.stdout, 'string', 'incus is not usable for the published lifecycle canary')

    const mutationEnv = { ...env, OPUTE_STANDALONE_ALLOW_MUTATIONS: 'true' }
    const mutationStart = await runLauncher(npm, version, cache, npmrc, mutationEnv, 'start', '--background')
    assert.equal(mutationStart.stdout.trim(), `${mcp}`)
    await waitHealth(health)
    const mutationState = { nextId: 1, sessionId: null, protocolVersion: '2024-11-05' }
    const mutationInitialize = await rpc(mcp, mutationState, 'initialize', {
      protocolVersion: mutationState.protocolVersion,
      capabilities: {},
      clientInfo: { name: 'published-npm-lifecycle-canary', version: '1' },
    })
    assert.equal(mutationInitialize.serverInfo.name, 'host-agent')
    await rpc(mcp, mutationState, 'notifications/initialized')

    const vmName = `opute-published-npm-e2e-${Date.now()}`
    let created = false
    try {
      assert.equal((await vmNames(mcp, mutationState)).has(vmName), false, `VM already exists: ${vmName}`)
      const create = await callTool(mcp, mutationState, 'create_vm', {
        vmName,
        image: 'ubuntu:22.04',
        cpus: 1,
        memory: '1GiB',
      })
      created = true
      await waitOperation(mcp, mutationState, operationId(create))
      assert.equal((await vmNames(mcp, mutationState)).has(vmName), true, `created VM missing: ${vmName}`)
      assert.equal((await callTool(mcp, mutationState, 'get_vm_info', { vmName, fast: true })).name, vmName)
      const remove = await callTool(mcp, mutationState, 'delete_vm', { vmName })
      await waitOperation(mcp, mutationState, operationId(remove))
      created = false
      assert.equal((await vmNames(mcp, mutationState)).has(vmName), false, `deleted VM remains: ${vmName}`)
      const remaining = await execFileAsync('incus', ['list', '--format', 'csv'], { timeout: 30_000 })
      assert.equal(remaining.stdout.includes(vmName), false, `deleted VM remains in Incus: ${vmName}`)
    } finally {
      if (created) {
        try {
          const remove = await callTool(mcp, mutationState, 'delete_vm', { vmName })
          await waitOperation(mcp, mutationState, operationId(remove), 300_000)
        } catch (error) {
          console.error(`cleanup failed for ${vmName}:`, error)
        }
      }
      await runLauncher(npm, version, cache, npmrc, mutationEnv, 'stop').catch(() => undefined)
    }
  } finally {
    await runLauncher(npm, version, cache, npmrc, env, 'stop').catch(() => undefined)
  }
})
