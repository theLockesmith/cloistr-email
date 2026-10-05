/**
 * Service hosts for this app, resolved once at module load.
 *
 * Every production hostname the frontend talks to goes through here, never a
 * local literal. The container writes /config.js at startup (see
 * nginx.conf.template) and index.html loads it before the bundle, so the same
 * image reports production hosts by default and staging hosts when deployed
 * with staging values. In the dev server /config.js 404s and the reader falls
 * back to VITE_* variables, then the production defaults.
 */
import { getServiceConfig } from '@cloistr/collab-common/config'

export const serviceConfig = getServiceConfig()

export const SIGNER_URL = serviceConfig.signerUrl
