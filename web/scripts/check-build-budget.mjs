import { readFileSync, readdirSync, statSync } from 'node:fs';
import { dirname, extname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { gzipSync } from 'node:zlib';

const webRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const distRoot = join(webRoot, 'dist');
const budget = JSON.parse(readFileSync(join(webRoot, 'performance-budget.json'), 'utf8'));
const indexHtml = readFileSync(join(distRoot, 'index.html'), 'utf8');

function referencedInitialAssets() {
  const assets = new Set();
  for (const match of indexHtml.matchAll(/(?:src|href)="([^"?#]+)(?:[?#][^"]*)?"/g)) {
    const pathname = match[1].replace(/^\//, '');
    if (pathname.startsWith('assets/') && ['.js', '.css'].includes(extname(pathname))) assets.add(pathname);
  }
  return [...assets].map((pathname) => join(distRoot, pathname));
}

function totals(files) {
  return files.reduce((result, file) => {
    const body = readFileSync(file);
    result.raw += body.byteLength;
    result.gzip += gzipSync(body, { level: 9 }).byteLength;
    return result;
  }, { raw: 0, gzip: 0 });
}

const initialAssets = referencedInitialAssets();
const javascript = totals(initialAssets.filter((file) => extname(file) === '.js'));
const css = totals(initialAssets.filter((file) => extname(file) === '.css'));
const fontFiles = readdirSync(join(distRoot, 'assets'))
  .filter((name) => name.endsWith('.woff2'))
  .map((name) => join(distRoot, 'assets', name));
const fontSizes = fontFiles.map((file) => statSync(file).size);

const measured = {
  initialJavaScriptRawBytes: javascript.raw,
  initialJavaScriptGzipBytes: javascript.gzip,
  initialCssRawBytes: css.raw,
  initialCssGzipBytes: css.gzip,
  largestFontSubsetBytes: Math.max(0, ...fontSizes),
  allFontSubsetsBytes: fontSizes.reduce((sum, size) => sum + size, 0),
};

const allowance = 1 + budget.allowedRegressionPercent / 100;
let failed = false;
for (const [metric, baseline] of Object.entries(budget.baseline)) {
  const limit = Math.ceil(baseline * allowance);
  const actual = measured[metric];
  const status = actual <= limit ? 'PASS' : 'FAIL';
  if (status === 'FAIL') failed = true;
  console.log(`${status} ${metric}: ${actual} bytes (baseline ${baseline}, ${budget.allowedRegressionPercent}% limit ${limit})`);
}

console.log(`Initial assets: ${initialAssets.length} files; font subsets: ${fontFiles.length} files`);
if (failed) {
  console.error('Production asset budget exceeded. Update the implementation or record an intentional new baseline with review evidence.');
  process.exitCode = 1;
}
