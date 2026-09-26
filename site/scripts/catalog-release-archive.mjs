import crypto from 'node:crypto'
import fs from 'node:fs'
import path from 'node:path'
import { isDeepStrictEqual } from 'node:util'

export function persistPreviousStableArchive(catalog, outputPath) {
  if (!catalog) return undefined
  if (!/^\d+\.\d+\.\d+$/.test(catalog.packageVersion || '')) {
    throw new Error('stable catalog archive requires a semantic package version')
  }

  const archiveDir = path.join(path.dirname(outputPath), 'release-archives')
  const archivePath = path.join(archiveDir, `v${catalog.packageVersion}.json`)
  const serialized = `${JSON.stringify(catalog, null, 2)}\n`
  const verifyExisting = () => {
    const existingStat = fs.lstatSync(archivePath)
    if (!existingStat.isFile() || existingStat.isSymbolicLink()) {
      throw new Error(`existing stable archive is not a regular file: v${catalog.packageVersion}`)
    }
    const existing = JSON.parse(fs.readFileSync(archivePath, 'utf8'))
    if (!isDeepStrictEqual(existing, catalog)) {
      throw new Error(`existing stable archive conflicts with v${catalog.packageVersion}`)
    }
  }

  fs.mkdirSync(archiveDir, { recursive: true })
  try {
    verifyExisting()
    return archivePath
  } catch (error) {
    if (error?.code !== 'ENOENT') throw error
  }

  const temporary = `${archivePath}.${process.pid}.${crypto.randomBytes(8).toString('hex')}.tmp`
  fs.writeFileSync(temporary, serialized, { flag: 'wx' })
  try {
    fs.linkSync(temporary, archivePath)
  } catch (error) {
    if (error?.code !== 'EEXIST') throw error
    verifyExisting()
  } finally {
    fs.unlinkSync(temporary)
  }
  return archivePath
}
