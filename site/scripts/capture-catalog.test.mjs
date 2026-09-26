import assert from 'node:assert/strict'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import test from 'node:test'
import { archivePreviousStableCatalog, selectCatalogRelease } from './catalog-release-policy.mjs'
import { persistPreviousStableArchive } from './catalog-release-archive.mjs'

const packageVersion = '0.2.1'
const catalogRevision = 'sha256:' + 'a'.repeat(64)
const checks = {
  explicitIdentity: true,
  openHealth: true,
  invalidTokenRejected: true,
  authenticatedDiscovery: true,
  authenticatedToolsList: true,
  structuredGetHostInfo: true,
  readOnly: true,
}
const publishedCanary = {
  packageVersion,
  catalogRevision,
  sourceSha: 'b'.repeat(40),
  runId: 123,
  runAttempt: 1,
  checks,
}
const stableCatalog = {
  packageName: '@opute/host-agent',
  packageVersion,
  releaseChannel: 'stable',
  catalogRevision,
  toolCount: 1,
  tools: [{ name: 'get_host_info' }],
  publishedCanary,
}

test('archives a fully verified prior stable release before a package version change', () => {
  assert.equal(archivePreviousStableCatalog(stableCatalog, '0.2.2', catalogRevision), stableCatalog)
})

test('keeps the existing exact stable version in place for the same snapshot', () => {
  assert.equal(archivePreviousStableCatalog(stableCatalog, packageVersion, catalogRevision), undefined)
})

test('refuses to replace a stable package version with a different catalog revision', () => {
  assert.throws(
    () => archivePreviousStableCatalog(stableCatalog, packageVersion, 'sha256:' + 'c'.repeat(64)),
    /stable package version cannot publish a different catalog revision/,
  )
})

test('does not archive an unverified preview candidate', () => {
  assert.equal(archivePreviousStableCatalog({ ...stableCatalog, releaseChannel: 'preview' }, '0.2.2', catalogRevision), undefined)
})

test('refuses to archive a stable catalog with incomplete canary evidence', () => {
  const incomplete = { ...stableCatalog, publishedCanary: { ...publishedCanary, checks: { ...checks, readOnly: false } } }
  assert.throws(() => archivePreviousStableCatalog(incomplete, '0.2.2', catalogRevision), /missing exact package and canary evidence/)
})

test('retains stable status only for the exact package and catalog canary', () => {
  assert.deepEqual(selectCatalogRelease({
    previous: stableCatalog,
    packageVersion,
    catalogRevision,
  }), { releaseChannel: 'stable', publishedCanary })
})

test('changed catalog revision returns to preview and drops old canary evidence', () => {
  const nextRevision = 'sha256:' + 'b'.repeat(64)
  assert.deepEqual(selectCatalogRelease({
    previous: stableCatalog,
    packageVersion,
    catalogRevision: nextRevision,
  }), { releaseChannel: 'preview', publishedCanary: undefined })
})

test('changed package version returns to preview and drops old canary evidence', () => {
  assert.deepEqual(selectCatalogRelease({
    previous: stableCatalog,
    packageVersion: '0.2.2',
    catalogRevision,
  }), { releaseChannel: 'preview', publishedCanary: undefined })
})

test('explicit stable promotion fails when the current catalog has no matching canary', () => {
  assert.throws(() => selectCatalogRelease({
    previous: stableCatalog,
    packageVersion,
    catalogRevision: 'sha256:' + 'b'.repeat(64),
    requestedChannel: 'stable',
  }), /matching published read-only canary evidence/)
})

test('rejects unknown requested channels', () => {
  assert.throws(() => selectCatalogRelease({
    previous: stableCatalog,
    packageVersion,
    catalogRevision,
    requestedChannel: 'latest',
  }), /release channel must be preview or stable/)
})

test('persists the prior stable snapshot atomically at its immutable version path', t => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'opute-catalog-archive-'))
  t.after(() => fs.rmSync(root, { recursive: true, force: true }))
  const outputPath = path.join(root, 'release-catalog.json')

  const archivePath = persistPreviousStableArchive(stableCatalog, outputPath)

  assert.equal(archivePath, path.join(root, 'release-archives', 'v0.2.1.json'))
  assert.deepEqual(JSON.parse(fs.readFileSync(archivePath, 'utf8')), stableCatalog)
  assert.equal(fs.readdirSync(path.dirname(archivePath)).length, 1)
})

test('accepts an identical existing stable archive without replacing its inode', t => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'opute-catalog-archive-'))
  t.after(() => fs.rmSync(root, { recursive: true, force: true }))
  const outputPath = path.join(root, 'release-catalog.json')
  const archivePath = persistPreviousStableArchive(stableCatalog, outputPath)
  const initial = fs.statSync(archivePath)

  assert.equal(persistPreviousStableArchive(stableCatalog, outputPath), archivePath)
  assert.equal(fs.statSync(archivePath).ino, initial.ino)
})

test('refuses a conflicting stable archive without overwriting it', t => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'opute-catalog-archive-'))
  t.after(() => fs.rmSync(root, { recursive: true, force: true }))
  const outputPath = path.join(root, 'release-catalog.json')
  const archiveDir = path.join(root, 'release-archives')
  const archivePath = path.join(archiveDir, 'v0.2.1.json')
  const conflicting = { ...stableCatalog, toolCount: 9 }
  fs.mkdirSync(archiveDir, { recursive: true })
  fs.writeFileSync(archivePath, JSON.stringify(conflicting))

  assert.throws(() => persistPreviousStableArchive(stableCatalog, outputPath), /existing stable archive conflicts/)
  assert.deepEqual(JSON.parse(fs.readFileSync(archivePath, 'utf8')), conflicting)
})

test('rejects archive paths supplied by an invalid package version', () => {
  assert.throws(
    () => persistPreviousStableArchive({ ...stableCatalog, packageVersion: '../escape' }, '/tmp/site/catalog.json'),
    /semantic package version/,
  )
})
