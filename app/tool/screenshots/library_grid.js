// Visual QA for #657. Uses actual library widgets with fixture API data.
// From app/: flutter build web --release --no-pub -t test/preview/library_grid_main.dart --output=/tmp/cantinarr-657-preview
// Then: node tool/screenshots/library_grid.js /tmp/cantinarr-657-preview /tmp/cantinarr-657-evidence
const { chromium } = require('playwright');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const http = require('node:http');
const zlib = require('node:zlib');
const build = path.resolve(process.argv[2]);
const output = path.resolve(process.argv[3]);
fs.mkdirSync(output, { recursive: true });
function crc32(bytes) {
  let crc = -1;
  for (const byte of bytes) {
    crc ^= byte;
    for (let bit = 0; bit < 8; bit++) crc = (crc >>> 1) ^ (0xedb88320 & -(crc & 1));
  }
  return (crc ^ -1) >>> 0;
}
function chunk(name, data) {
  const type = Buffer.from(name);
  const length = Buffer.alloc(4); length.writeUInt32BE(data.length);
  const crc = Buffer.alloc(4); crc.writeUInt32BE(crc32(Buffer.concat([type, data])));
  return Buffer.concat([length, type, data, crc]);
}
function artwork() {
  const width = 120, height = 180;
  const pixels = Buffer.alloc(height * (1 + width * 4));
  for (let y = 0; y < height; y++) for (let x = 0; x < width; x++) {
    const i = y * (1 + width * 4) + 1 + x * 4;
    const color = (Math.floor(x / 30) + Math.floor(y / 30)) % 2
      ? [20, 210, 130, 255] : [210, 70, 200, 255];
    pixels.set(color, i);
  }
  const header = Buffer.alloc(13);
  header.writeUInt32BE(width); header.writeUInt32BE(height, 4); header[8] = 8; header[9] = 6;
  return Buffer.concat([Buffer.from([137,80,78,71,13,10,26,10]),
    chunk('IHDR', header), chunk('IDAT', zlib.deflateSync(pixels)), chunk('IEND', Buffer.alloc(0))]);
}

const png = artwork();
const imageReads = [];
const server = http.createServer((req, res) => {
  const url = new URL(req.url, 'http://localhost');
  if (url.pathname.includes('/MediaCover/')) {
    imageReads.push({ path: url.pathname, authorized: req.headers.authorization === 'Bearer library-fixture' });
    if (req.headers.authorization !== 'Bearer library-fixture') { res.writeHead(401); return res.end(); }
    res.writeHead(200, { 'Content-Type': 'image/png' });
    return res.end(png);
  }
  let target = path.resolve(build, '.' + decodeURIComponent(url.pathname));
  if (!target.startsWith(build + path.sep) && target !== build) { res.writeHead(403); return res.end(); }
  if (url.pathname === '/' || !fs.existsSync(target)) target = path.join(build, 'index.html');
  const mime = { '.html': 'text/html', '.js': 'text/javascript', '.wasm': 'application/wasm',
    '.json': 'application/json', '.png': 'image/png', '.woff2': 'font/woff2', '.ttf': 'font/ttf' };
  res.writeHead(200, { 'Content-Type': mime[path.extname(target)] || 'application/octet-stream' });
  fs.createReadStream(target).pipe(res);
});

(async () => {
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const base = `http://127.0.0.1:${server.address().port}`;
  const browser = await chromium.launch({ channel: 'chrome', headless: true });
  const report = [];
  const artworkCache = new Map();
  try {
    for (const width of [390, 1440]) {
      const context = await browser.newContext({ viewport: { width, height: width < 600 ? 844 : 1000 } });
      // CDN photos are visual fixtures. Fetch outside browser CORS, preserving
      // actual image content; authenticated library artwork uses the server.
      await context.route(/https:\/\/(image.tmdb.org|covers.openlibrary.org)\//, async route => {
        const url = route.request().url();
        try {
          if (!artworkCache.has(url)) artworkCache.set(url, fetch(url, { signal: AbortSignal.timeout(15000) })
            .then(async response => ({ status: response.status, body: Buffer.from(await response.arrayBuffer()) })));
          const result = await artworkCache.get(url);
          await route.fulfill({ status: result.status, body: result.body,
            headers: { 'content-type': 'image/jpeg', 'access-control-allow-origin': '*' } });
        } catch (_) { await route.fulfill({ status: 404, body: '' }); }
      });
      for (const module of ['radarr', 'sonarr', 'chaptarr', 'lidarr']) {
        const page = await context.newPage();
        const errors = [];
        page.on('pageerror', error => errors.push(error.message));
        await page.goto(`${base}/?module=${module}`);
        const grid = page.getByRole('button', { name: /^Grid/ });
        await grid.waitFor({ timeout: 60000 });
        await page.waitForTimeout(1500);
        await page.screenshot({ path: path.join(output, `${module}-${width}-list.png`) });
        await grid.click();
        await page.waitForTimeout(2500);
        await page.mouse.move(0, 0);
        await page.waitForTimeout(800);
        await page.screenshot({ path: path.join(output, `${module}-${width}-grid.png`) });
        assert.equal(errors.length, 0, `${module}: ${errors.join('; ')}`);
        await page.reload();
        await grid.waitFor({ timeout: 60000 });
        await page.waitForTimeout(700);
        assert.equal(await grid.getAttribute('aria-current'), 'true', `${module}: grid preference did not survive reload`);
        report.push({ module, width, errors, savedGrid: true });
        console.log('PASS', module, width);
        await page.close();
      }
      await context.close();
    }
    assert(imageReads.length > 0 && imageReads.every(read => read.authorized), 'Instance artwork must retain authentication');
    fs.writeFileSync(path.join(output, 'results.json'), JSON.stringify({ report, imageReads }, null, 2));
  } finally {
    await browser.close();
    await new Promise(resolve => server.close(resolve));
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
