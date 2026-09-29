import assert from 'node:assert/strict';
import { mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { tmpdir } from 'node:os';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import test from 'node:test';

const checker = fileURLToPath(new URL('./check-internal-links.mjs', import.meta.url));

test('accepts existing base-path pages, assets, and fragments', async (t) => {
  const site = await temporarySite(t);
  await mkdir(path.join(site, 'docs', 'reference'), { recursive: true });
  await mkdir(path.join(site, 'assets'), { recursive: true });
  await writeFile(
    path.join(site, 'index.html'),
    '<a href="/go-ring/docs/reference/">Reference</a><img src="/go-ring/assets/logo.svg">',
  );
  await writeFile(path.join(site, 'docs', 'reference', 'index.html'), '<h1 id="operation">Operation</h1>');
  await writeFile(path.join(site, 'assets', 'logo.svg'), '<svg></svg>');

  const result = runChecker(site);
  assert.equal(result.status, 0, `${result.stdout}\n${result.stderr}`);
});

test('rejects missing targets, fragments, and links outside the base path', async (t) => {
  const site = await temporarySite(t);
  await writeFile(
    path.join(site, 'index.html'),
    '<a href="/go-ring/missing/">Missing page</a><a href="/go-ring/#missing">Missing fragment</a><a href="/elsewhere/">Wrong base path</a>',
  );

  const result = runChecker(site);
  assert.equal(result.status, 1, `${result.stdout}\n${result.stderr}`);
  assert.match(result.stderr, /missing internal target/);
  assert.match(result.stderr, /missing fragment/);
  assert.match(result.stderr, /outside base path/);
});

async function temporarySite(t) {
  const root = await mkdtemp(path.join(tmpdir(), 'go-ring-doc-links-'));
  const site = path.join(root, 'site');
  await mkdir(site);
  t.after(async () => rm(root, { recursive: true, force: true }));
  return site;
}

function runChecker(site) {
  return spawnSync(process.execPath, [checker, site, '/go-ring'], {
    encoding: 'utf8',
    timeout: 10_000,
  });
}
