#!/usr/bin/env node
/**
 * take-screenshots.mjs
 * Captures key wizard + dashboard screens and assembles them into
 * docs/screenshots/demo.gif (3 s per frame).
 *
 * Usage:
 *   node scripts/take-screenshots.mjs
 *
 * Requires:
 *   - app already built (run scripts/reload.sh first)
 *   - npx playwright and Chromium installed
 *   - ImageMagick `convert` on PATH
 */

import { chromium } from 'playwright';
import { execSync, spawn } from 'child_process';
import { writeFileSync, readFileSync } from 'fs';
import { resolve, dirname } from 'path';
import { fileURLToPath } from 'url';

const __dir = dirname(fileURLToPath(import.meta.url));
const PROJECT = resolve(__dir, '..');
const DATA_DIR = resolve(PROJECT, 'data');
const SHOTS_DIR = resolve(PROJECT, 'docs', 'screenshots');
const STATE_FILE = resolve(DATA_DIR, 'wizard_state.json');
const BINARY = resolve(PROJECT, 'unifi-cert-smash-deck');
const PORT = 8105;
const BASE = `http://127.0.0.1:${PORT}`;

// ── helpers ──────────────────────────────────────────────────────────────────

function sleep(ms) { return new Promise(r => setTimeout(r, ms)); }

async function waitForApp(timeoutMs = 10_000) {
  const start = Date.now();
  while (Date.now() - start < timeoutMs) {
    try {
      const res = await fetch(`${BASE}/api/health`);
      if (res.ok) return;
    } catch { /* not up yet */ }
    await sleep(200);
  }
  throw new Error('App did not start within timeout');
}

function killApp() {
  try { execSync(`pkill -f "${BINARY}"`, { stdio: 'ignore' }); } catch {}
}

function startApp() {
  const env = { ...process.env, PORT: String(PORT) };
  const child = spawn(BINARY, [], {
    detached: true,
    stdio: 'ignore',
    env,
    cwd: PROJECT,
  });
  child.unref();
}

function writeState(state) {
  writeFileSync(STATE_FILE, JSON.stringify(state, null, 2), { mode: 0o600 });
}

async function restartWithState(state) {
  writeState(state);
  killApp();
  await sleep(400);
  startApp();
  await waitForApp();
}

// ── wizard states for each scene ─────────────────────────────────────────────

const BASE_STATE = {
  version: 2,
  current_step: 0,
  udm_host: '192.168.1.201',
  udm_port: 22,
  ssh_user: 'root',
  ssh_key_path: '/home/nick/.ssh/id_ed25519',
  ssh_known_hosts: '/home/nick/.ssh/known_hosts_unifi',
  key_generated: false,
  udm_os_version: '',
  udm_le_state: 'unknown',
  current_cert_cn: '',
  current_cert_days: 0,
  current_cert_self_signed: false,
  cert_hosts: 'unifi.example.com',
  cert_email: 'admin@example.com',
  dns_provider: 'cloudflare',
  dns_zone: '',
  staging_mode: false,
  install_action: '',
  issued_cert_cn: '',
  issued_cert_expiry: '0001-01-01T00:00:00Z',
  issued_by_le: false,
  started_at: '0001-01-01T00:00:00Z',
  completed_at: '0001-01-01T00:00:00Z',
};

const NOW = new Date().toISOString();

const SCENES = [
  {
    name: '01_dashboard',
    url: '/',
    desc: 'Dashboard / cert health',
    state: {
      ...BASE_STATE,
      current_step: 5, // done
      results: {
        0: { step_num: 0, status: 'passed', finished_at: NOW },
        1: { step_num: 1, status: 'passed', finished_at: NOW },
        2: { step_num: 2, status: 'passed', finished_at: NOW },
        3: { step_num: 3, status: 'passed', finished_at: NOW },
        4: { step_num: 4, status: 'passed', finished_at: NOW },
      },
      udm_le_state: 'healthy',
      issued_cert_cn: 'unifi.example.com',
      issued_cert_expiry: new Date(Date.now() + 89 * 86_400_000).toISOString(),
      issued_by_le: true,
      completed_at: NOW,
    },
  },
  {
    name: '02_wizard_connect',
    url: '/wizard',
    desc: 'Step 1 — Connect',
    state: { ...BASE_STATE, current_step: 0, results: {} },
  },
  {
    name: '03_wizard_domain',
    url: '/wizard',
    desc: 'Step 2 — Domain & DNS',
    state: {
      ...BASE_STATE,
      current_step: 1,
      results: {
        0: { step_num: 0, status: 'passed', finished_at: NOW },
      },
    },
  },
  {
    name: '04_wizard_preflight',
    url: '/wizard',
    desc: 'Step 3 — Preflight checks',
    state: {
      ...BASE_STATE,
      current_step: 2,
      results: {
        0: { step_num: 0, status: 'passed', finished_at: NOW },
        1: { step_num: 1, status: 'passed', finished_at: NOW },
      },
    },
  },
  {
    name: '05_wizard_install',
    url: '/wizard',
    desc: 'Step 4 — Install',
    state: {
      ...BASE_STATE,
      current_step: 3,
      results: {
        0: { step_num: 0, status: 'passed', finished_at: NOW },
        1: { step_num: 1, status: 'passed', finished_at: NOW },
        2: { step_num: 2, status: 'passed', finished_at: NOW },
      },
    },
  },
  {
    name: '06_wizard_verify',
    url: '/wizard',
    desc: 'Step 5 — Verify',
    state: {
      ...BASE_STATE,
      current_step: 4,
      results: {
        0: { step_num: 0, status: 'passed', finished_at: NOW },
        1: { step_num: 1, status: 'passed', finished_at: NOW },
        2: { step_num: 2, status: 'passed', finished_at: NOW },
        3: { step_num: 3, status: 'passed', finished_at: NOW },
      },
      udm_le_state: 'healthy',
      issued_cert_cn: 'unifi.example.com',
      issued_cert_expiry: new Date(Date.now() + 89 * 86_400_000).toISOString(),
      issued_by_le: true,
    },
  },
];

// ── main ─────────────────────────────────────────────────────────────────────

const screenshots = [];

const browser = await chromium.launch({ headless: true });

for (const scene of SCENES) {
  console.log(`📸  ${scene.name} — ${scene.desc}`);

  await restartWithState(scene.state);

  const ctx = await browser.newContext({
    viewport: { width: 1100, height: 820 },
    colorScheme: 'dark',
  });
  const page = await ctx.newPage();

  // Force dark class before any paint
  await page.addInitScript(() => {
    document.documentElement.classList.add('dark');
    localStorage.setItem('unificert-theme', 'dark');
  });

  await page.goto(`${BASE}${scene.url}`, { waitUntil: 'load', timeout: 15_000 });
  await sleep(1200); // let HTMX settle / auto-preflight fire

  const outPath = resolve(SHOTS_DIR, `${scene.name}.png`);
  await page.screenshot({ path: outPath, fullPage: false });
  screenshots.push(outPath);
  console.log(`   saved → ${outPath}`);

  await ctx.close();
}

await browser.close();

// ── assemble GIF via ffmpeg (reliable per-frame delay; ImageMagick loses delay
//    when using grouped resize args) ──────────────────────────────────────────
console.log('\n🎞   Assembling demo.gif via ffmpeg…');
const gifPath = resolve(SHOTS_DIR, 'demo.gif');
// 0.333 fps ≈ 3 s per frame
execSync(
  `ffmpeg -y -framerate 0.333 -pattern_type glob -i '${SHOTS_DIR}/0*.png' ` +
  `-vf "scale=900:-1:flags=lanczos,split[s0][s1];[s0]palettegen=128:stats_mode=single[p];[s1][p]paletteuse=dither=bayer:bayer_scale=3" ` +
  `-loop 0 "${gifPath}"`,
  { stdio: 'inherit', shell: '/bin/bash' }
);
console.log(`✓  ${gifPath}`);

// ── restore real state ────────────────────────────────────────────────────────
console.log('\n🔄  Restoring app state…');
const realState = JSON.parse(readFileSync(resolve(DATA_DIR, 'unificert-settings.json'), 'utf8'));
// Reset wizard to step 0 so the real app resumes normally
writeState(BASE_STATE);
killApp();
await sleep(400);
startApp();
await waitForApp();
console.log('✓  App restored at http://127.0.0.1:8105/');
