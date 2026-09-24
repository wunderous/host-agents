#!/usr/bin/env node
// Capture the standalone public catalog through its authenticated MCP wire.
// Only explicitly approved descriptor fields are written to the site context.
import crypto from 'node:crypto'
import fs from 'node:fs'
import net from 'node:net'
import os from 'node:os'
import path from 'node:path'
import { spawn } from 'node:child_process'
import { fileURLToPath } from 'node:url'

const scriptDir = path.dirname(fileURLToPath(import.meta.url))
const siteDir = path.resolve(scriptDir, '..')
const root = path.resolve(siteDir, '..')
const packagePath = path.join(root, 'npm', 'local-host-agent', 'package.json')
const outputPath = path.resolve(process.env.SITE_CATALOG_OUTPUT || path.join(siteDir, 'context', 'release-catalog.json'))
const binary = path.resolve(process.env.OPUTE_HOST_AGENT_BINARY || path.join(root, 'dist', 'opute-host-agent'))
const packageInfo = JSON.parse(fs.readFileSync(packagePath, 'utf8'))
const previous = fs.existsSync(outputPath) ? JSON.parse(fs.readFileSync(outputPath, 'utf8')) : {}

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

function cleanEnv() {
  const env = {}
  for (const [key, value] of Object.entries(process.env)) {
    if (!key.startsWith('OPUTE_') && !['MCP_AUTH_TOKEN', 'NPM_TOKEN', 'NODE_AUTH_TOKEN'].includes(key)) {
      env[key] = value
    }
  }
  return env
}

async function requestRpc(url, state, method, params = {}) {
  const headers = {
    Accept: 'application/json, text/event-stream',
    'Content-Type': 'application/json',
    'MCP-Protocol-Version': '2026-07-28',
    'Mcp-Method': method,
    Origin: new URL(url).origin,
    Authorization: `Bearer ${state.token}`,
  }
  if (method === 'tools/call' && params.name) headers['Mcp-Name'] = params.name
  const body = { jsonrpc: '2.0', method, id: state.nextId++ }
  body.params = {
    ...params,
    _meta: {
      'io.modelcontextprotocol/protocolVersion': '2026-07-28',
      'io.modelcontextprotocol/clientCapabilities': {},
      'io.modelcontextprotocol/clientInfo': { name: 'opute-site-catalog-export', version: '1' },
    },
  }
  const response = await fetch(url, { method: 'POST', headers, body: JSON.stringify(body) })
  if (!response.ok) throw new Error(`${method} returned HTTP ${response.status}`)
  const text = await response.text()
  if (!text.trim()) throw new Error(`${method} returned an empty body`)
  const envelope = JSON.parse(text)
  if (envelope.error) throw new Error(`${method} returned an MCP error`)
  return envelope.result || {}
}

async function callTool(url, state, name, arguments_ = {}) {
  const result = await requestRpc(url, state, 'tools/call', { name, arguments: arguments_ })
  if (result.isError === true || !result.structuredContent || typeof result.structuredContent !== 'object') {
    throw new Error(`${name} did not return structured content`)
  }
  return result.structuredContent
}

function approvedDescriptor(tool) {
  if (!tool || typeof tool.name !== 'string' || !tool.name || typeof tool.effect !== 'string') {
    throw new Error('catalog contains a descriptor without a public name or effect')
  }
  if (!tool.inputSchema || typeof tool.inputSchema !== 'object' || tool.inputSchema.type !== 'object') {
    throw new Error(`catalog descriptor ${tool.name} has no object input schema`)
  }
  const allowedEffects = new Set(['read', 'mutation', 'destructive', 'credential_bearing'])
  if (!allowedEffects.has(tool.effect)) throw new Error(`catalog descriptor ${tool.name} has unknown effect`)
  const descriptor = {
    name: tool.name,
    description: String(tool.description || ''),
    effect: tool.effect,
    idempotent: tool.idempotent === true,
    inputSchema: tool.inputSchema,
  }
  if (tool.title) descriptor.title = String(tool.title)
  if (Number.isSafeInteger(tool.version) && tool.version > 0) descriptor.version = tool.version
  if (typeof tool.capabilityId === 'string' && tool.capabilityId) descriptor.capabilityId = tool.capabilityId
  if (tool.requiresApproval === true) descriptor.requiresApproval = true
  if (tool.outputSchema && typeof tool.outputSchema === 'object') descriptor.outputSchema = tool.outputSchema
  return descriptor
}

async function main() {
  if (!fs.existsSync(binary)) throw new Error(`Host Agent binary not found: ${binary}`)
  const port = await freePort()
  const token = crypto.randomBytes(32).toString('hex')
  const agentId = crypto.randomUUID()
  const runtimeDir = fs.mkdtempSync(path.join(os.tmpdir(), 'opute-site-catalog-'))
  const env = {
    ...cleanEnv(),
    OPUTE_REMOTE_AGENT_ID: agentId,
    OPUTE_INFRA_PROVIDER_ID: 'incus',
    OPUTE_LOCAL_HOST_AGENT_RUNTIME_DIR: path.join(runtimeDir, 'runtime'),
    OPUTE_STANDALONE_STATE_DIR: path.join(runtimeDir, 'state'),
    HOST_MCP_BIND_HOST: '127.0.0.1',
    HOST_MCP_PORT: String(port),
    MCP_AUTH_TOKEN: token,
  }
  const child = spawn(binary, ['--mode=standalone', '--transport=http'], {
    cwd: runtimeDir,
    env,
    stdio: ['ignore', 'ignore', 'pipe'],
  })
  let stderr = ''
  child.stderr.setEncoding('utf8')
  child.stderr.on('data', chunk => { stderr = `${stderr}${chunk}`.slice(-8_000) })
  const healthUrl = `http://127.0.0.1:${port}/health`
  const mcpUrl = `http://127.0.0.1:${port}/mcp`
  try {
    const deadline = Date.now() + 20_000
    let ready = false
    while (Date.now() < deadline) {
      try {
        const response = await fetch(healthUrl)
        if (response.ok) {
          const health = await response.json()
          if (health.ok === true && health.agentId === agentId) {
            ready = true
            break
          }
        }
      } catch {}
      if (child.exitCode !== null) throw new Error(`Host Agent exited with ${child.exitCode}`)
      await new Promise(resolve => setTimeout(resolve, 100))
    }
    if (!ready) throw new Error('Host Agent did not become healthy before catalog export')

    const state = { nextId: 1, token }
    const discovery = await requestRpc(mcpUrl, state, 'server/discover')
    if (discovery.resultType !== 'complete') throw new Error('server/discover did not complete')
    const listed = await requestRpc(mcpUrl, state, 'tools/list')
    const listedNames = new Set((Array.isArray(listed.tools) ? listed.tools : []).map(tool => tool?.name))
    if (!listedNames.size || !listedNames.has('get_host_info')) throw new Error('tools/list omitted get_host_info')

    const catalog = await callTool(mcpUrl, state, 'get_capability_catalog')
    if (typeof catalog.catalogRevision !== 'string' || !catalog.catalogRevision.startsWith('sha256:')) {
      throw new Error('get_capability_catalog returned no revisioned snapshot')
    }
    if (!Array.isArray(catalog.tools)) throw new Error('get_capability_catalog returned no descriptors')
    const descriptors = catalog.tools
      .filter(tool => listedNames.has(tool?.name))
      .map(approvedDescriptor)
      .sort((left, right) => left.name < right.name ? -1 : left.name > right.name ? 1 : 0)
    const descriptorNames = new Set(descriptors.map(tool => tool.name))
    const missing = [...listedNames].filter(name => !descriptorNames.has(name))
    if (missing.length) throw new Error(`catalog descriptors omitted tools/list names: ${missing.join(', ')}`)
    if (descriptorNames.size !== listedNames.size) throw new Error('catalog descriptor set differs from tools/list')

    const priorVersionMatches = previous.packageVersion === packageInfo.version
    const releaseChannel = process.env.SITE_CATALOG_CHANNEL || (priorVersionMatches ? previous.releaseChannel : 'preview')
    const publishedCanary = priorVersionMatches ? previous.publishedCanary : undefined
    if (!['preview', 'stable'].includes(releaseChannel)) throw new Error('release channel must be preview or stable')
    if (releaseChannel === 'stable' && publishedCanary?.packageVersion !== packageInfo.version) {
      throw new Error('stable catalog requires matching published read-only canary evidence')
    }

    const output = {
      packageName: packageInfo.name,
      packageVersion: packageInfo.version,
      releaseChannel,
      catalogRevision: catalog.catalogRevision,
      toolCount: descriptors.length,
      tools: descriptors,
      ...(publishedCanary ? { publishedCanary } : {}),
    }
    fs.mkdirSync(path.dirname(outputPath), { recursive: true })
    fs.writeFileSync(outputPath, `${JSON.stringify(output, null, 2)}\n`)
    console.log(`captured ${descriptors.length} public tool descriptors at ${catalog.catalogRevision}`)
  } catch (error) {
    if (stderr.trim()) error.message += `\nHost Agent diagnostic: ${stderr.trim()}`
    throw error
  } finally {
    child.kill('SIGTERM')
    await new Promise(resolve => {
      const timeout = setTimeout(resolve, 2_000)
      child.once('close', () => { clearTimeout(timeout); resolve() })
    })
  }
}

main().catch(error => {
  console.error(error.message)
  process.exitCode = 1
})
