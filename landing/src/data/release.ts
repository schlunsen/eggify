// Desktop download shown on the landing page. Resolved at build time:
// the published manifest (<PUBLIC_DOWNLOAD_BASE_URL>/latest.json, written by
// `just desktop-deploy`) wins; without network it falls back to the version in
// internal/version/version.go. The DMG link is the stable alias of the newest
// release, so it stays right even if the page is older than the release.
import versionGo from '../../../internal/version/version.go?raw';

// PUBLIC_DOWNLOAD_BASE_URL (build-time env, e.g. https://example.com/download) names the
// release folder; unset, links are site-relative (/download/…) and the manifest is not fetched.
const DOWNLOAD_BASE = ((import.meta.env.PUBLIC_DOWNLOAD_BASE_URL as string | undefined) ?? '/download').replace(/\/$/, '');
export const DMG_URL = `${DOWNLOAD_BASE}/Eggify.dmg`;
const MANIFEST_URL = `${DOWNLOAD_BASE}/latest.json`;

function versionFromSource(): string {
  return versionGo.match(/Version\s*=\s*"([^"]+)"/)?.[1] ?? '';
}

async function versionFromManifest(): Promise<string> {
  try {
    if (!/^https?:/.test(MANIFEST_URL)) return '';
    const res = await fetch(MANIFEST_URL, { signal: AbortSignal.timeout(5000) });
    if (!res.ok) return '';
    const m = (await res.json()) as { version?: unknown };
    return typeof m.version === 'string' && /^\d+(\.\d+){0,3}$/.test(m.version) ? m.version : '';
  } catch {
    return '';
  }
}

export const DESKTOP_VERSION = (await versionFromManifest()) || versionFromSource() || '2.0.0';
