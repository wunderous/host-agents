#!/usr/bin/env node

const fs = require('node:fs')
const os = require('node:os')
const path = require('node:path')
const crypto = require('node:crypto')
const https = require('node:https')
const zlib = require('node:zlib')
const { spawn } = require('node:child_process')

const packageVersion = require('./package.json').version

function targetName() {
  const platform = process.platform
  const arch = process.arch
  if (platform === 'linux' && arch === 'x64') return 'linux-x64'
  if (platform === 'linux' && arch === 'arm64') return 'linux-arm64'
  if (platform === 'win32' && arch === 'x64') return 'windows-x64'
  throw new Error(`unsupported local host-agent target: ${platform}/${arch}`)
}

function request(url, redirects = 0) {
  if (redirects > 5) return Promise.reject(new Error('too many artifact redirects'))
  return new Promise((resolve, reject) => {
    https.get(url, { headers: { 'User-Agent': '@opute/local-host-agent' } }, response => {
      if ([301, 302, 307, 308].includes(response.statusCode) && response.headers.location) {
        response.resume()
        resolve(request(new URL(response.headers.location, url).toString(), redirects + 1))
        return
      }
      if (response.statusCode !== 200) {
        response.resume()
        reject(new Error(`host-agent download failed: HTTP ${response.statusCode}`))
        return
      }
      const chunks = []
      response.on('data', chunk => chunks.push(chunk))
      response.on('end', () => resolve(Buffer.concat(chunks)))
      response.on('error', reject)
    }).on('error', reject)
  })
}

async function resolveBinary() {
  if (process.env.OPUTE_HOST_AGENT_BINARY) return process.env.OPUTE_HOST_AGENT_BINARY
  const target = targetName()
  const cacheDir = process.env.OPUTE_HOST_AGENT_CACHE_DIR || path.join(os.homedir(), '.cache', 'opute', 'host-agent')
  const binaryName = process.platform === 'win32' ? `host-agent-${target}.exe` : `host-agent-${target}`
  const binaryPath = path.join(cacheDir, packageVersion, binaryName)
  if (!fs.existsSync(binaryPath)) {
    const base = (process.env.OPUTE_HOST_AGENT_RELEASE_BASE_URL || 'https://github.com/opute-io/host-agents/releases/download').replace(/\/$/, '')
    const archive = await request(`${base}/v${packageVersion}/host-agent-${target}.gz`)
    const expected = process.env.OPUTE_HOST_AGENT_SHA256
    if (expected) {
      const actual = crypto.createHash('sha256').update(archive).digest('hex')
      if (actual !== expected.toLowerCase()) throw new Error('host-agent artifact checksum mismatch')
    } else if (process.env.OPUTE_HOST_AGENT_REQUIRE_CHECKSUM === 'true') {
      throw new Error('OPUTE_HOST_AGENT_SHA256 is required when checksum enforcement is enabled')
    }
    fs.mkdirSync(cacheDir, { recursive: true, mode: 0o700 })
    const temporary = `${binaryPath}.download-${process.pid}`
    fs.mkdirSync(path.dirname(binaryPath), { recursive: true, mode: 0o700 })
    fs.writeFileSync(temporary, zlib.gunzipSync(archive), { mode: 0o700 })
    fs.renameSync(temporary, binaryPath)
  }
  return binaryPath
}

async function main() {
  const binary = await resolveBinary()
  const args = process.argv.slice(2)
  const child = spawn(binary, ['--mode=standalone', '--transport=stdio', ...args], {
    stdio: 'inherit',
    env: {
      ...process.env,
      OPUTE_AGENT_MODE: 'standalone',
      OPUTE_TRANSPORT: 'stdio',
    },
  })
  child.on('error', error => {
    console.error(`failed to start host agent: ${error.message}`)
    process.exitCode = 1
  })
  child.on('exit', (code, signal) => {
    process.exitCode = code ?? (signal ? 1 : 0)
  })
}

main().catch(error => {
  console.error(error.message)
  process.exitCode = 1
})
