// Visual QA for #656 using the real router, shell, and request screens.
// flutter build web --release --no-pub -t test/preview/screenshot_main.dart --output=/tmp/cantinarr-656-preview
// node tool/screenshots/request_history.js /tmp/cantinarr-656-preview /tmp/cantinarr-656-evidence
const { chromium } = require('playwright');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const http = require('node:http');
const build = path.resolve(process.argv[2]);
const output = path.resolve(process.argv[3]);
fs.mkdirSync(output, { recursive: true });
const server = http.createServer((req, res) => {
  const url = new URL(req.url, 'http://localhost');
  let target = path.resolve(build, '.' + decodeURIComponent(url.pathname));
  if (!target.startsWith(build + path.sep) && target !== build) {
    res.writeHead(403); return res.end();
  }
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
  const artwork = new Map();
  try {
    for (const width of [340, 390, 1440]) {
      if (process.argv[4] && width !== Number(process.argv[4])) continue;
      const context = await browser.newContext({ viewport: { width, height: width < 600 ? 844 : 1000 } });
      await context.route(/https:\/\/image.tmdb.org\//, async route => {
        const url = route.request().url();
        try {
          if (!artwork.has(url)) artwork.set(url, fetch(url, { signal: AbortSignal.timeout(10000) })
            .then(async r => ({ status: r.status, body: Buffer.from(await r.arrayBuffer()) })));
          const result = await artwork.get(url);
          await route.fulfill({ ...result, headers: { 'content-type': 'image/jpeg', 'access-control-allow-origin': '*' } });
        } catch (_) { await route.fulfill({ status: 404, body: '' }); }
      });
      const page = await context.newPage();
      const errors = [];
      page.on('pageerror', e => errors.push(e.message));
      await page.goto(`${base}/?shot=history-${width}#/approvals`);
      const accessibility = page.locator('flt-semantics-placeholder');
      await accessibility.waitFor({ state: 'attached', timeout: 60000 });
      await accessibility.evaluate(element => element.click());
      const history = page.getByRole('button', { name: 'History', exact: true });
      await history.waitFor({ timeout: 60000 });
      await page.evaluate(() => {
        document.getElementById('splash')?.remove();
        document.getElementById('splash-branding')?.remove();
      });
      await page.waitForTimeout(1800);
      await page.screenshot({ path: path.join(output, `approvals-${width}.png`) });
      await history.click();
      const sharedBook = page.getByRole('button', { name: /Project Hail Mary/ });
      await page.waitForTimeout(800);
      await sharedBook.waitFor();
      await page.waitForTimeout(1500);
      await page.screenshot({ path: path.join(output, `history-${width}.png`) });
      await sharedBook.click();
      await page.getByText('No reviewer recorded', { exact: true }).waitFor();
      await page.waitForTimeout(600);
      await page.screenshot({ path: path.join(output, `detail-${width}.png`) });
      await page.keyboard.press('Escape');
      await page.waitForTimeout(600);
      await page.getByRole('button', { name: 'Load older requests' }).click();
      await page.getByRole('button', { name: /Kind of Blue/ }).waitFor();
      const search = page.getByRole('textbox', { name: 'Search titles' });
      await search.click();
      await page.keyboard.type('Dune', { delay: 100 });
      await page.keyboard.press('Enter');
      await page.waitForTimeout(800);
      await page.screenshot({ path: path.join(output, `search-${width}.png`) });
      await sharedBook.waitFor({ state: 'hidden' });
      await page.getByRole('button', { name: /^Dune/ }).waitFor();
      assert.deepEqual(errors, [], `browser errors at ${width}`);
      report.push({ width, headerNavigation: true, details: true, pagination: true, search: true, errors });
      await context.close();
      console.log('PASS', width);
    }
    fs.writeFileSync(path.join(output, 'report.json'), JSON.stringify(report, null, 2));
  } finally {
    await browser.close();
    await new Promise(resolve => server.close(resolve));
  }
})().catch(error => { console.error(error); process.exitCode = 1; server.close(); });
