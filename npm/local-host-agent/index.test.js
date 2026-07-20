const assert = require('node:assert/strict')
const crypto = require('node:crypto')
const fs = require('node:fs')
const http = require('node:http')
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

  await execFileAsync(process.execPath, [indexPath], { env, timeout: 10_000 })
  assert.equal(archiveRequests, 1)
  const cached = path.join(cacheDir, packageVersion, 'host-agent-linux-x64')
  fs.appendFileSync(cached, 'corruption')
  await execFileAsync(process.execPath, [indexPath], { env, timeout: 10_000 })
  assert.equal(archiveRequests, 2)
})

test('native Windows fails before attempting an artifact download', { skip: process.platform !== 'win32' }, async () => {
  await assert.rejects(
    execFileAsync(process.execPath, [indexPath], {
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
  await execFileAsync(process.execPath, [entry], {
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
