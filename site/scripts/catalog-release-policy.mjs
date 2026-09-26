const RELEASE_CHANNELS = new Set(['preview', 'stable'])
const REQUIRED_CANARY_CHECKS = new Set([
  'explicitIdentity',
  'openHealth',
  'invalidTokenRejected',
  'authenticatedDiscovery',
  'authenticatedToolsList',
  'structuredGetHostInfo',
  'readOnly',
])
const CATALOG_KEYS = new Set([
  'packageName',
  'packageVersion',
  'releaseChannel',
  'catalogRevision',
  'toolCount',
  'tools',
  'publishedCanary',
])

export function archivePreviousStableCatalog(previous, nextVersion, nextCatalogRevision) {
  if (!previous || previous.releaseChannel !== 'stable') return undefined
  if (previous.packageVersion === nextVersion) {
    if (previous.catalogRevision !== nextCatalogRevision) {
      throw new Error('a stable package version cannot publish a different catalog revision')
    }
    return undefined
  }

  const evidence = previous.publishedCanary
  const checks = evidence?.checks
  if (
    Object.keys(previous).some(key => !CATALOG_KEYS.has(key)) ||
    previous.packageName !== '@opute/host-agent' ||
    typeof previous.packageVersion !== 'string' ||
    !/^\d+\.\d+\.\d+$/.test(previous.packageVersion) ||
    !/^sha256:[0-9a-f]{64}$/.test(previous.catalogRevision || '') ||
    !Array.isArray(previous.tools) ||
    previous.toolCount !== previous.tools.length ||
    !evidence ||
    Object.keys(evidence).some(key => !new Set([
      'packageVersion', 'catalogRevision', 'sourceSha', 'runId', 'runAttempt', 'checks',
    ]).has(key)) ||
    evidence.packageVersion !== previous.packageVersion ||
    evidence.catalogRevision !== previous.catalogRevision ||
    !/^[0-9a-f]{40}$/.test(evidence.sourceSha || '') ||
    !Number.isSafeInteger(evidence.runId) || evidence.runId < 1 ||
    !Number.isSafeInteger(evidence.runAttempt) || evidence.runAttempt < 1 ||
    !checks ||
    Object.keys(checks).length !== REQUIRED_CANARY_CHECKS.size ||
    Object.keys(checks).some(key => !REQUIRED_CANARY_CHECKS.has(key)) ||
    [...REQUIRED_CANARY_CHECKS].some(key => checks[key] !== true)
  ) {
    throw new Error('previous stable catalog is missing exact package and canary evidence')
  }
  return previous
}

export function selectCatalogRelease({ previous, packageVersion, catalogRevision, requestedChannel }) {
  const sameSnapshot = previous.packageVersion === packageVersion && previous.catalogRevision === catalogRevision
  const priorCanary = previous.publishedCanary
  const publishedCanary = sameSnapshot &&
    priorCanary?.packageVersion === packageVersion &&
    priorCanary?.catalogRevision === catalogRevision
    ? priorCanary
    : undefined
  const releaseChannel = requestedChannel || (sameSnapshot ? previous.releaseChannel : 'preview')

  if (!RELEASE_CHANNELS.has(releaseChannel)) throw new Error('release channel must be preview or stable')
  if (releaseChannel === 'stable' && !publishedCanary) {
    throw new Error('stable catalog requires matching published read-only canary evidence')
  }

  return { releaseChannel, publishedCanary }
}
