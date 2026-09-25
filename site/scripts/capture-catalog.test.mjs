import assert from 'node:assert/strict'
import test from 'node:test'
import { selectCatalogRelease } from './catalog-release-policy.mjs'

const packageVersion = '0.2.1'
const catalogRevision = 'sha256:' + 'a'.repeat(64)
const publishedCanary = { packageVersion, catalogRevision, runId: 123, runAttempt: 1 }
const stableCatalog = { packageVersion, catalogRevision, releaseChannel: 'stable', publishedCanary }

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
