const RELEASE_CHANNELS = new Set(['preview', 'stable'])

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
