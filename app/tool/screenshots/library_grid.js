// Visual QA for the four library modules. Uses actual widgets with fixture API data.
// From app/: flutter build web --release --no-pub -t test/preview/library_grid_main.dart --output=/tmp/cantinarr-library-preview
// Then: node tool/screenshots/library_grid.js /tmp/cantinarr-library-preview /tmp/cantinarr-library-evidence
const { chromium } = require('playwright');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const http = require('node:http');
const zlib = require('node:zlib');
const build = path.resolve(process.argv[2]);
const output = path.resolve(process.argv[3]);
const actionsOnly = process.argv.includes('--actions');
const sortOnly = process.argv.includes('--sort');
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
function artwork(variant) {
  const width = 120, height = 180;
  const palettes = [
    [[34, 48, 72], [141, 92, 100], [240, 180, 126]],
    [[28, 58, 67], [68, 128, 119], [215, 188, 141]],
    [[52, 42, 78], [123, 79, 132], [223, 160, 171]],
  ];
  const [dark, mid, light] = palettes[variant % palettes.length];
  const pixels = Buffer.alloc(height * (1 + width * 4));
  for (let y = 0; y < height; y++) for (let x = 0; x < width; x++) {
    const i = y * (1 + width * 4) + 1 + x * 4;
    const fade = y / height;
    const glow = Math.max(0, 1 - Math.hypot((x - 70) / 70, (y - 85) / 85));
    const ring = Math.abs(Math.hypot(x - 52, y - 83) - 42) < 3 ? 0.42 : 0;
    const color = [0, 1, 2].map(channel => Math.min(255, Math.round(
      dark[channel] * (1 - fade) + mid[channel] * fade +
      (light[channel] - mid[channel]) * (glow * 0.7 + ring)
    )));
    color.push(255);
    pixels.set(color, i);
  }
  const header = Buffer.alloc(13);
  header.writeUInt32BE(width); header.writeUInt32BE(height, 4); header[8] = 8; header[9] = 6;
  return Buffer.concat([Buffer.from([137,80,78,71,13,10,26,10]),
    chunk('IHDR', header), chunk('IDAT', zlib.deflateSync(pixels)), chunk('IEND', Buffer.alloc(0))]);
}

const pngs = [0, 1, 2].map(artwork);
const imageReads = [];
const server = http.createServer((req, res) => {
  const url = new URL(req.url, 'http://localhost');
  if (url.pathname.includes('/MediaCover/')) {
    imageReads.push({ path: url.pathname, authorized: req.headers.authorization === 'Bearer library-fixture' });
    if (req.headers.authorization !== 'Bearer library-fixture') { res.writeHead(401); return res.end(); }
    res.writeHead(200, { 'Content-Type': 'image/png' });
    const variant = Number(url.pathname.match(/\/(\d+)\.jpg$/)?.[1] || 0);
    return res.end(pngs[variant % pngs.length]);
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
    for (const width of (actionsOnly ? [390, 1440] : sortOnly ? [320, 390, 1440] : [340, 390, 1440])) {
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
        const scaleQuery = sortOnly && width === 390 ? '&scale=1.7' : '';
        await page.goto(`${base}/?module=${module}${scaleQuery}`);
        const grid = page.getByRole('button', { name: /^Grid/ });
        await grid.waitFor({ timeout: 60000 });
        await page.waitForTimeout(1500);
        if (sortOnly) {
          const sort = page.getByRole('button', { name: /^Sort:/ });
          const filter = page.getByRole('button', { name: /^Filter (movies|series|authors|artists)$/ });
          const positions = await Promise.all([filter, sort, grid].map(locator => locator.boundingBox()));
          assert(positions[0].x < positions[1].x && positions[1].x < positions[2].x,
            `${module}: sort must sit between filter and layout`);
          await page.screenshot({ path: path.join(output, `${module}-${width}-sort-toolbar.png`) });
          await sort.click();
          await page.getByRole('menuitem', { name: /Date Added/ }).waitFor();
          await page.waitForTimeout(300);
          await page.screenshot({ path: path.join(output, `${module}-${width}-sort-menu.png`) });
          await page.getByRole('menuitem', { name: /Date Added/ }).click();
          await page.getByRole('button', { name: 'Sort: Date Added, ascending', exact: true }).waitFor();
          await sort.click();
          await page.getByRole('menuitem', { name: /Date Added/ }).click();
          await page.getByRole('button', { name: 'Sort: Date Added, descending', exact: true }).waitFor();
          await sort.click();
          await page.waitForTimeout(300);
          await page.screenshot({ path: path.join(output, `${module}-${width}-sort-descending.png`) });
          await page.keyboard.press('Escape');
          await grid.click();
          await page.waitForTimeout(400);
          await page.screenshot({ path: path.join(output, `${module}-${width}-sorted-grid.png`) });
          await page.reload();
          await page.getByRole('button', { name: 'Sort: Date Added, descending', exact: true }).waitFor({ timeout: 60000 });
          if (width < 600) {
            await page.goto(`${base}/?module=${module}&scroll=1${scaleQuery}`);
            await sort.waitFor({ timeout: 60000 });
            await page.waitForTimeout(700);
            const expanded = await sort.boundingBox();
            await page.mouse.move(width / 2, 650);
            await page.mouse.wheel(0, 500);
            await page.waitForTimeout(600);
            const collapsed = await sort.boundingBox();
            assert(collapsed.y < expanded.y - 70, `${module}: sort toolbar did not stay visible after collapse`);
            await page.screenshot({ path: path.join(output, `${module}-${width}-sort-collapsed.png`) });
          }
          assert.equal(errors.length, 0, `${module}: ${errors.join('; ')}`);
          report.push({ module, width, textScale: width === 390 ? 1.7 : 1,
            sortPlacement: true, reversal: true, savedSort: true, scrollCollapse: width < 600, errors });
          console.log('PASS sorts', module, width);
          await page.close();
          continue;
        }
        if (actionsOnly) {
          for (const mode of ['list', 'grid']) {
            if (mode === 'grid') {
              await grid.click();
              await page.waitForTimeout(600);
            }
            await page.getByRole('button', { name: /^Actions for / }).first().click();
            await page.waitForTimeout(700);
            await page.getByRole('menuitem', { name: 'Rescan files', exact: true }).waitFor();
            await page.waitForTimeout(300);
            await page.screenshot({ path: path.join(output, `${module}-${width}-${mode}-menu.png`) });
            for (const label of ['Automatic search', 'Interactive search', 'Refresh metadata', 'Rescan files', 'Remove…']) {
              assert.equal(await page.getByRole('menuitem', { name: label, exact: true }).count(), 1);
            }
            await page.keyboard.press('Escape');
            await page.waitForTimeout(300);
          }
          assert.equal(errors.length, 0, `${module}: ${errors.join('; ')}`);
          if (module === 'chaptarr' || module === 'lidarr') {
            await page.getByRole('button', { name: /^Actions for / }).first().click();
            await page.getByRole('menuitem', { name: module === 'chaptarr' ? 'Edit author' : 'Edit artist', exact: true }).click();
            await page.getByRole('button', { name: /^Quality profile/ }).first().waitFor();
            await page.waitForTimeout(500);
            await page.screenshot({ path: path.join(output, `${module}-${width}-settings.png`) });
          }
          report.push({ module, width, actionMenus: true, errors });
          console.log('PASS menus', module, width);
          await page.close();
          continue;
        }
        await page.screenshot({ path: path.join(output, `${module}-${width}-list.png`) });
        const beforeToggle = await page.getByRole('textbox').boundingBox();
        await grid.click();
        await page.waitForTimeout(2500);
        const afterToggle = await page.getByRole('textbox').boundingBox();
        assert(Math.abs(afterToggle.y - beforeToggle.y) < 2,
          `${module}: switching views at the top moved the library header`);
        await page.mouse.move(0, 0);
        await page.waitForTimeout(800);
        await page.screenshot({ path: path.join(output, `${module}-${width}-grid.png`) });
        assert.equal(errors.length, 0, `${module}: ${errors.join('; ')}`);
        await page.reload();
        const savedViewControl = width < 600
          ? page.getByRole('button', { name: /^List view$/ })
          : grid;
        await savedViewControl.waitFor({ timeout: 60000 });
        await page.waitForTimeout(700);
        if (width >= 600) {
          assert.equal(await grid.getAttribute('aria-current'), 'true', `${module}: grid preference did not survive reload`);
        } else {
          assert.equal(await grid.count(), 0, `${module}: grid preference did not survive reload`);
        }
        if (width < 600) {
          await page.goto(`${base}/?module=${module}&scroll=1`);
          await page.getByRole('button', { name: /^List view$/ }).waitFor({ timeout: 60000 });
          await page.waitForTimeout(1200);
          const field = page.getByRole('textbox');
          const expanded = await field.boundingBox();
          await page.mouse.move(width / 2, 650);
          await page.mouse.wheel(0, 500);
          await page.waitForTimeout(600);
          const collapsed = await field.boundingBox();
          assert(collapsed.y < expanded.y - 100, `${module}: summary did not collapse`);
          await page.screenshot({ path: path.join(output, `${module}-${width}-collapsed.png`) });
          await page.mouse.wheel(0, -100);
          await page.waitForTimeout(600);
          const restored = await field.boundingBox();
          assert(Math.abs(restored.y - expanded.y) < 1, `${module}: summary did not return on upward scroll`);
        }
        assert.equal(errors.length, 0, `${module}: ${errors.join('; ')}`);
        report.push({ module, width, errors, savedGrid: true, scrollCollapse: width < 600 });
        console.log('PASS', module, width);
        await page.close();
      }
      await context.close();
    }
    assert((actionsOnly || sortOnly || imageReads.length > 0) && imageReads.every(read => read.authorized), 'Instance artwork must retain authentication');
    fs.writeFileSync(path.join(output, 'results.json'), JSON.stringify({ report, imageReads }, null, 2));
  } finally {
    await browser.close();
    await new Promise(resolve => server.close(resolve));
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
