// Release-mode #650 regression: real Flutter screens, browser history and
// image decoding. API fixtures are local; this is not live-provider proof.
// From app/: flutter build web --release --no-pub \
//   -t test/preview/discover_return_main.dart --output=/tmp/discover-return-web
// From this directory: node discover_return.js /tmp/discover-return-web /tmp/discover-return-evidence
const { chromium, firefox } = require('playwright');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const http = require('node:http');
const zlib = require('node:zlib');
const build = path.resolve(process.argv[2] || '/tmp/discover-return-web');
const output = path.resolve(process.argv[3] || '/tmp/discover-return-evidence');
fs.mkdirSync(output, { recursive: true });
let mode = {};
const reads = [];
const images = [];

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
const server = http.createServer(async (req, res) => {
  const url = new URL(req.url, 'http://localhost');
  if (url.pathname === '/__fixture/read') {
    const apiPath = url.searchParams.get('path');
    reads.push({ path: apiPath, page: url.searchParams.get('page') });
    const controlled = /^\/api\/(discover|instances)\//.test(apiPath);
    const current = controlled ? { ...mode } : {};
    if (current.delay) await new Promise(resolve => setTimeout(resolve, current.delay));
    res.writeHead(200, { 'Content-Type': 'application/json' });
    return res.end(JSON.stringify(current));
  }
  if (url.pathname.startsWith('/fixture-images/') || url.pathname.startsWith('/api/trakt/images/')) {
    const authenticated = url.pathname.startsWith('/api/');
    images.push({ path: url.pathname, authenticated, authorized: req.headers.authorization === 'Bearer browser-fixture' });
    if (authenticated && req.headers.authorization !== 'Bearer browser-fixture') {
      res.writeHead(401); return res.end();
    }
    // A repeated HTTP request cannot hide in the browser's HTTP cache.
    res.writeHead(200, { 'Content-Type': 'image/png', 'Cache-Control': 'no-store' });
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

async function compareArtwork(page, before, after) {
  return page.evaluate(async ({ before, after }) => {
    async function pixels(data) {
      const image = new Image(); image.src = 'data:image/png;base64,' + data;
      await image.decode();
      const canvas = document.createElement('canvas'); canvas.width = image.width; canvas.height = image.height;
      const ctx = canvas.getContext('2d'); ctx.drawImage(image, 0, 0);
      return ctx.getImageData(0, 0, canvas.width, canvas.height).data;
    }
    const a = await pixels(before), b = await pixels(after);
    let artwork = 0, retained = 0;
    const near = (data, i, color) => color.every((c, j) => Math.abs(data[i + j] - c) <= 4);
    for (let i = 0; i < a.length; i += 4) {
      if (near(a, i, [20,210,130]) || near(a, i, [210,70,200])) {
        artwork++;
        if ([0,1,2].every(j => Math.abs(a[i+j] - b[i+j]) <= 6)) retained++;
      }
    }
    return { artworkPixels: artwork, retainedPixels: retained, fraction: retained / artwork };
  }, { before: before.toString('base64'), after: after.toString('base64') });
}

(async () => {
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const base = `http://127.0.0.1:${server.address().port}`;
  console.log('Fixture server:', base);
  const report = [];
  try {
    for (const [name, engine, options] of [
      ['chrome', chromium, { channel: 'chrome' }], ['firefox', firefox, {}],
    ]) {
      const browser = await engine.launch({ headless: true, ...options });
      try {
        for (const route of (process.argv.includes('--detail-only') ? [] :
          ['/dashboard/movies', '/dashboard/tv', '/browse/movie/featured', '/browse/tv/featured'])) {
          mode = {};
          const page = await browser.newPage({ viewport: { width: 1440, height: 1080 }, deviceScaleFactor: 1 });
          const warnings = [];
          let phase = 'initial';
          page.on('console', msg => { if (/Resource has no data|Uploading zeros/.test(msg.text())) warnings.push({ phase, text: msg.text() }); });
          const stem = `${name}-${route.split('/').filter(Boolean).join('-')}`;
          await page.goto(`${base}/#${route}`);
          await page.locator('flt-semantics').first().waitFor({ timeout: 60000 });
          await page.waitForTimeout(2000);
          await page.mouse.move(0, 0);
          const before = await page.screenshot({ path: path.join(output, `${stem}-before.png`) });
          const baseline = await compareArtwork(page, before, before);
          assert(baseline.artworkPixels > 15000, `${stem}: fixture artwork did not load (${baseline.artworkPixels})`);
          const cases = [];
          for (const refreshMode of [{}, { delay: 1800 }, { status: 503 }]) {
            // Both dashboard hero and grid cards open the real detail route.
            const title = route.includes('/tv') ? /House of the Dragon/ : /The Super Mario Galaxy Movie/;
            const target = page.getByRole('button', { name: title }).first();
            phase = 'detail';
            const bounds = await target.boundingBox();
            assert(bounds && bounds.x >= 280 && bounds.y >= 0 &&
              bounds.x + bounds.width <= 1440 && bounds.y + bounds.height <= 1080,
              `${stem}: selected artwork must already be visible without scrolling`);
            await target.click();
            await page.waitForURL(/#\/detail\//);
            await page.waitForTimeout(900);
            const imageCount = images.length;
            const readCount = reads.length;
            mode = refreshMode;
            phase = 'back';
            await page.goBack();
            await page.waitForURL(url => url.hash === `#${route}`);
            await page.mouse.move(0, 0);
            await page.waitForTimeout(650);
            const after = await page.screenshot({ path: path.join(output, `${stem}-back-${cases.length}.png`) });
            const pixels = await compareArtwork(page, before, after);
            assert(pixels.fraction > 0.99, `${stem}: artwork changed/blanked on Back: ${JSON.stringify(pixels)}`);
            assert.equal(images.length, imageCount, `${stem}: unchanged artwork fetched again on Back`);
            assert(reads.length > readCount, `${stem}: Back did not refresh metadata`);
            cases.push({ mode: refreshMode, ...pixels, additionalImageReads: images.length - imageCount });
            // Let the slow/failed refresh settle before the next navigation.
            await page.waitForTimeout(refreshMode.delay ? 4200 : 900);
          }
          if (warnings.length) console.log(stem, JSON.stringify(warnings));
          assert.equal(warnings.filter(w => w.phase === 'back').length, 0, `${stem}: empty WebGL uploads on Back`);
          const evidence = { browser: name, version: browser.version(), route, cases, warnings };
          report.push(evidence);
          fs.writeFileSync(path.join(output, 'results.json'), JSON.stringify(report, null, 2));
          console.log('PASS', stem, JSON.stringify(cases.map(c => c.fraction)));
          await page.close();
        }
        // Keep both related rows and the hero visible so Back is checked
        // before any scroll can repaint a broken browser texture.
        mode = {};
        const page = await browser.newPage({ viewport: { width: 1440, height: 2400 }, deviceScaleFactor: 1 });
        const route = '/detail/movie/687163';
        const stem = `${name}-movie-detail-related`;
        const warnings = [];
        let phase = 'initial';
        page.on('console', msg => { if (/Resource has no data|Uploading zeros/.test(msg.text())) warnings.push({ phase, text: msg.text() }); });
        await page.goto(`${base}/#${route}`);
        await page.locator('flt-semantics').first().waitFor({ timeout: 60000 });
        await page.waitForTimeout(2200);
        await page.mouse.move(0, 0);
        const heroClip = { x: 280, y: 90, width: 1160, height: 300 };
        const before = await page.screenshot({ clip: heroClip, path: path.join(output, `${stem}-before.png`) });
        const baseline = await compareArtwork(page, before, before);
        assert(baseline.artworkPixels > 15000, `${stem}: hero artwork did not load (${baseline.artworkPixels})`);
        const cases = [];
        for (const section of ['Recommended', 'Similar', 'Recommended']) {
          const target = page.getByRole('button', { name: /The Super Mario Galaxy Movie/ })
            .nth(section === 'Recommended' ? 0 : 1);
          const bounds = await target.boundingBox();
          assert(bounds && bounds.y >= 0 && bounds.y + bounds.height <= 2400,
            `${stem}: ${section} must be visible without scrolling`);
          phase = 'detail';
          await target.click();
          await page.waitForURL(/#\/detail\/movie\/1226863/);
          await page.waitForTimeout(1500);
          await page.mouse.move(0, 0);
          const relatedHero = await page.screenshot({ clip: heroClip });
          const relatedPixels = await compareArtwork(page, relatedHero, relatedHero);
          assert(relatedPixels.artworkPixels > 15000, `${stem}: authenticated related hero did not load`);
          const imageCount = images.length;
          phase = 'back';
          await page.goBack();
          await page.waitForURL(url => url.hash === `#${route}`);
          await page.mouse.move(0, 0);
          await page.waitForTimeout(1100);
          const after = await page.screenshot({ clip: heroClip, path: path.join(output, `${stem}-back-${cases.length}.png`) });
          const pixels = await compareArtwork(page, before, after);
          assert(pixels.fraction > 0.99, `${stem}: ${section} hero blanked on Back: ${JSON.stringify(pixels)}`);
          assert.equal(images.length, imageCount, `${stem}: unchanged hero fetched again on Back`);
          cases.push({ section, ...pixels, additionalImageReads: images.length - imageCount });
        }
        assert.equal(warnings.filter(w => w.phase === 'back').length, 0, `${stem}: empty hero texture on Back`);
        report.push({ browser: name, version: browser.version(), route, cases, warnings });
        fs.writeFileSync(path.join(output, 'results.json'), JSON.stringify(report, null, 2));
        console.log('PASS', stem, JSON.stringify(cases.map(c => c.fraction)));
        await page.close();
      } finally { await browser.close(); }
    }
    assert(images.some(i => i.authenticated), 'Authenticated artwork was not exercised');
    assert(images.some(i => !i.authenticated), 'Public artwork was not exercised');
    assert(images.filter(i => i.authenticated).every(i => i.authorized), 'Artwork lost authentication');
    fs.writeFileSync(path.join(output, 'requests.json'), JSON.stringify({ reads, images }, null, 2));
  } finally { await new Promise(resolve => server.close(resolve)); }
})().catch(error => { console.error(error); process.exitCode = 1; });
