// Uses real routed screens with the isolated screenshot HTTP fixture.
// flutter build web --release --no-pub -t test/preview/screenshot_main.dart --output=/tmp/cantinarr-discord-preview
// node tool/screenshots/discord.js /tmp/cantinarr-discord-preview /tmp/cantinarr-discord-evidence
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
  let file = path.resolve(build, '.' + decodeURIComponent(url.pathname));
  if (!file.startsWith(build + path.sep) && file !== build) { res.writeHead(403); return res.end(); }
  if (url.pathname === '/' || !fs.existsSync(file)) file = path.join(build, 'index.html');
  const types = { '.html': 'text/html', '.js': 'text/javascript', '.wasm': 'application/wasm',
    '.json': 'application/json', '.png': 'image/png', '.woff2': 'font/woff2', '.ttf': 'font/ttf' };
  res.writeHead(200, { 'Content-Type': types[path.extname(file)] || 'application/octet-stream' });
  fs.createReadStream(file).pipe(res);
});

(async () => {
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const base = `http://127.0.0.1:${server.address().port}`;
  const browser = await chromium.launch({ channel: 'chrome', headless: true });
  const report = [];
  try {
    for (const width of [320, 390, 1440]) {
      const context = await browser.newContext({ viewport: { width, height: width < 600 ? 844 : 1000 } });
      const page = await context.newPage();
      await page.routeWebSocket(/\/api\/ws$/, () => {});
      const errors = [];
      const reveal = async locator => {
        for (let attempt = 0; attempt < 24; attempt++) {
          const box = await locator.count() > 0
            ? await locator.first().boundingBox({ timeout: 1000 }).catch(() => null)
            : null;
          if (box && box.y >= 125 && box.y + box.height < page.viewportSize().height - 20) return;
          await page.mouse.move(width < 600 ? width - 35 : width * 0.65, page.viewportSize().height * 0.7);
          await page.mouse.wheel(0, box && box.y < 125 ? -260 : 260);
          await page.waitForTimeout(180);
        }
        await page.screenshot({ path: path.join(output, `failed-scroll-${width}.png`) });
        throw new Error(`Could not reveal control: ${locator}`);
      };
      page.on('pageerror', error => errors.push(error.message));
      page.on('console', message => {
        if (message.type() === 'error' || message.text().includes('Exception')) {
          errors.push(message.text());
          console.error(width, message.text());
        }
      });
      await page.goto(`${base}/?shot=discord-${width}#/settings/discord-notifications`);
      const semantics = page.locator('flt-semantics-placeholder');
      await semantics.waitFor({ state: 'attached', timeout: 60000 });
      await semantics.evaluate(element => element.click());
      await page.getByRole('textbox', { name: 'Discord user IDs' }).waitFor({ timeout: 60000 });
      await page.evaluate(() => {
        document.getElementById('splash')?.remove();
        document.getElementById('splash-branding')?.remove();
      });
      await page.waitForTimeout(500);
      await page.screenshot({ path: path.join(output, `personal-${width}.png`) });
      await page.getByRole('button', { name: /Server Discord Notifications/ }).click();
      await page.getByRole('textbox', { name: 'Replace webhook URL' }).waitFor();
      await page.waitForTimeout(500);
      await page.screenshot({ path: path.join(output, `server-${width}.png`) });
      await page.getByRole('button', { name: /^Events/ }).click();
      await reveal(page.getByRole('switch', { name: /^Requested content available/ }));
      await page.waitForTimeout(350);
      await page.screenshot({ path: path.join(output, `events-${width}.png`) });
      await reveal(page.getByRole('button', { name: /^Thread and appearance/ }));
      await page.getByRole('button', { name: /^Thread and appearance/ }).click();
      await reveal(page.getByRole('textbox', { name: 'Discord thread ID' }));
      await page.waitForTimeout(350);
      await page.screenshot({ path: path.join(output, `appearance-${width}.png`) });
      assert.deepEqual(errors, [], `browser errors at ${width}`);
      report.push({ width, personalPage: true, serverNavigation: true, events: true, appearance: true, errors });
      await context.close();
      console.log('PASS', width);
    }
    fs.writeFileSync(path.join(output, 'report.json'), JSON.stringify(report, null, 2));
  } finally {
    await browser.close();
    await new Promise(resolve => server.close(resolve));
  }
})().catch(error => { console.error(error); process.exitCode = 1; server.close(); });
