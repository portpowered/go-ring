import { readdir, readFile, stat } from 'node:fs/promises';
import path from 'node:path';
import process from 'node:process';

const [siteArgument, basePathArgument] = process.argv.slice(2);

if (!siteArgument || basePathArgument === undefined) {
  console.error('Usage: node tools/docs/check-internal-links.mjs <site-directory> <base-path>');
  process.exit(2);
}

const siteRoot = path.resolve(siteArgument);
const basePath = normalizeBasePath(basePathArgument);
const origin = 'https://docs.invalid';
const failures = [];

async function main() {
  const htmlFiles = await listHTMLFiles(siteRoot);
  if (htmlFiles.length === 0) throw new Error(`No rendered HTML pages found in ${siteRoot}`);

  for (const htmlFile of htmlFiles) {
    const source = await readFile(htmlFile, 'utf8');
    const pageURL = pageURLForFile(htmlFile);

    for (const link of collectLinks(source)) {
      await checkLink(htmlFile, pageURL, link);
    }
  }

  if (failures.length > 0) {
    for (const failure of failures) console.error(failure);
    throw new Error(`${failures.length} broken rendered-site link(s) found across ${htmlFiles.length} pages.`);
  }

  console.log(`Checked internal links across ${htmlFiles.length} rendered pages; all targets exist.`);
}

function normalizeBasePath(value) {
  const normalized = value.replace(/\/+$/, '');
  if (normalized !== '' && (!normalized.startsWith('/') || normalized === '/')) {
    throw new Error(`base-path must be empty or an absolute URL path: ${value}`);
  }

  return normalized;
}

async function listHTMLFiles(root, directory = root) {
  const files = [];

  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const entryPath = path.join(directory, entry.name);
    if (entry.isDirectory()) files.push(...await listHTMLFiles(root, entryPath));
    else if (entry.isFile() && entry.name.toLowerCase().endsWith('.html')) files.push(entryPath);
  }

  return files;
}

function pageURLForFile(htmlFile) {
  const relativeFile = path.relative(siteRoot, htmlFile).split(path.sep).join('/');
  let route = relativeFile;

  if (route === 'index.html') route = '';
  else if (route.endsWith('/index.html')) route = route.slice(0, -'index.html'.length);

  return new URL(`${basePath}/${route}`, origin);
}

function collectLinks(source) {
  const links = [];
  const elementPattern = /<(?:a|area|base|link|script|img|source|video|audio|iframe|form|input|object|embed|track|use)\b[^>]*>/gi;
  const attributePattern = /\b(href|src|poster|action)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))/gi;
  const markup = source
    .replace(/<!--[\s\S]*?-->/g, '')
    .replace(/(<script\b[^>]*>)[\s\S]*?<\/script\s*>/gi, '$1')
    .replace(/(<style\b[^>]*>)[\s\S]*?<\/style\s*>/gi, '$1');

  for (const element of markup.matchAll(elementPattern)) {
    for (const attribute of element[0].matchAll(attributePattern)) {
      const value = attribute[2] ?? attribute[3] ?? attribute[4] ?? '';
      links.push(decodeHTMLEntities(value));
    }
  }

  return links;
}

function decodeHTMLEntities(value) {
  return value.replace(/&(#x[0-9a-f]+|#\d+|amp|lt|gt|quot|apos);/gi, (entity, code) => {
    if (code.toLowerCase() === 'amp') return '&';
    if (code.toLowerCase() === 'lt') return '<';
    if (code.toLowerCase() === 'gt') return '>';
    if (code.toLowerCase() === 'quot') return '"';
    if (code.toLowerCase() === 'apos') return "'";

    const numeric = code[1]?.toLowerCase() === 'x'
      ? Number.parseInt(code.slice(2), 16)
      : Number.parseInt(code.slice(1), 10);
    return Number.isFinite(numeric) ? String.fromCodePoint(numeric) : entity;
  });
}

async function checkLink(sourceFile, pageURL, link) {
  if (link === '' || link.startsWith('//') || /^[a-z][a-z0-9+.-]*:/i.test(link)) return;

  let targetURL;
  try {
    targetURL = new URL(link, pageURL);
  } catch {
    failures.push(`${relativeDisplayPath(sourceFile)}: invalid URL ${JSON.stringify(link)}`);
    return;
  }

  if (targetURL.origin !== origin) return;

  let decodedPath;
  try {
    decodedPath = decodeURIComponent(targetURL.pathname);
  } catch {
    failures.push(`${relativeDisplayPath(sourceFile)}: invalid path encoding in ${JSON.stringify(link)}`);
    return;
  }

  if (basePath !== '' && decodedPath !== basePath && !decodedPath.startsWith(`${basePath}/`)) {
    failures.push(`${relativeDisplayPath(sourceFile)}: internal URL is outside base path ${basePath}: ${JSON.stringify(link)}`);
    return;
  }

  const relativeTarget = basePath === '' ? decodedPath.replace(/^\/+/, '') : decodedPath.slice(basePath.length).replace(/^\/+/, '');
  const targetPath = path.resolve(siteRoot, relativeTarget || 'index.html');
  const relativeTargetPath = path.relative(siteRoot, targetPath);

  if (relativeTargetPath === '..' || relativeTargetPath.startsWith(`..${path.sep}`) || path.isAbsolute(relativeTargetPath)) {
    failures.push(`${relativeDisplayPath(sourceFile)}: internal URL escapes site output: ${JSON.stringify(link)}`);
    return;
  }

  const targetFile = await resolveTargetFile(targetPath, decodedPath);
  if (!targetFile) {
    failures.push(`${relativeDisplayPath(sourceFile)}: missing internal target ${JSON.stringify(link)}`);
    return;
  }

  if (targetURL.hash !== '' && targetFile.toLowerCase().endsWith('.html')) {
    let fragment;
    try {
      fragment = decodeURIComponent(targetURL.hash.slice(1));
    } catch {
      failures.push(`${relativeDisplayPath(sourceFile)}: invalid fragment encoding in ${JSON.stringify(link)}`);
      return;
    }

    if (fragment !== '') {
      const targetHTML = await readFile(targetFile, 'utf8');
      if (!hasFragment(targetHTML, fragment)) {
        failures.push(`${relativeDisplayPath(sourceFile)}: missing fragment #${fragment} in ${JSON.stringify(link)}`);
      }
    }
  }
}

async function resolveTargetFile(targetPath, decodedPath) {
  const candidates = [];

  try {
    const targetInfo = await stat(targetPath);
    if (targetInfo.isDirectory()) candidates.push(path.join(targetPath, 'index.html'));
    else if (targetInfo.isFile()) candidates.push(targetPath);
  } catch (error) {
    if (error?.code !== 'ENOENT' && error?.code !== 'ENOTDIR') throw error;
  }

  if (decodedPath.endsWith('/') || path.extname(targetPath) === '') {
    candidates.push(path.join(targetPath, 'index.html'));
  }

  for (const candidate of candidates) {
    try {
      if ((await stat(candidate)).isFile()) return candidate;
    } catch (error) {
      if (error?.code !== 'ENOENT' && error?.code !== 'ENOTDIR') throw error;
    }
  }

  return undefined;
}

function hasFragment(html, fragment) {
  const escaped = fragment.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const fragmentPattern = new RegExp(`\\b(?:id|name)=["']${escaped}["']`, 'i');
  return fragmentPattern.test(html);
}

function relativeDisplayPath(file) {
  return path.relative(siteRoot, file).split(path.sep).join('/');
}

main().catch((error) => {
  console.error(error.message);
  process.exitCode = 1;
});
