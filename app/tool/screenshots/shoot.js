// Store-screenshot runner: drives the Flutter web screenshot harness at exact
// store pixel sizes using system Chrome. Usage: node shoot.js <baseUrl> <outDir>
const { chromium } = require('playwright');
const fs = require('fs');
const path = require('path');

const baseUrl = process.argv[2] || 'http://localhost:8787';
const outRoot = process.argv[3] || path.join(__dirname, 'out');
const onlyDevice = process.argv[4]; // optional device tag filter for iteration
const onlyShot = process.argv[5]; // optional shot name filter for iteration

// viewport * dpr = exact store size:
//  iphone69: 440x956 @3  -> 1320x2868 (App Store 6.9")
//  ipad13:   1032x1376 @2 -> 2064x2752 (App Store iPad 13")
//  android:  360x800 @3  -> 1080x2400 (Play phone)
//  tablet10: 800x1280 @2 -> 1600x2560 (Play 10" tablet)
const devices = [
  { tag: 'iphone69', vw: 440, vh: 956, dpr: 3 },
  { tag: 'ipad13', vw: 1032, vh: 1376, dpr: 2 },
  { tag: 'android', vw: 360, vh: 800, dpr: 3 },
  { tag: 'tablet10', vw: 800, vh: 1280, dpr: 2 },
];

// Filled in once the harness lands; `actions` gets (page, device) after load.
const shots = require('./routes.js');

(async () => {
  const browser = await chromium.launch({ channel: 'chrome' });
  for (const d of devices) {
    if (onlyDevice && d.tag !== onlyDevice) continue;
    const ctx = await browser.newContext({
      viewport: { width: d.vw, height: d.vh },
      deviceScaleFactor: d.dpr,
      isMobile: d.tag === 'android' || d.tag === 'iphone69',
      hasTouch: true,
    });
    const dir = path.join(outRoot, d.tag);
    fs.mkdirSync(dir, { recursive: true });
    let i = 0;
    for (const s of shots) {
      if (s.skip && s.skip.includes(d.tag)) continue;
      i += 1;
      if (onlyShot && s.name !== onlyShot) continue;
      // Fresh page + unique query string per shot: hash-only navigation does
      // not reliably re-route the SPA, and leftover overlay/scroll state must
      // not bleed between shots.
      const page = await ctx.newPage();
      const url = `${baseUrl}/?shot=${s.name}#${s.route}`;
      await page.goto(url, { waitUntil: 'networkidle' }).catch(() => {});
      await page.waitForTimeout(s.settle || 4500);
      if (s.actions) await s.actions(page, d);
      // Let poster images finish decoding after any interaction.
      await page.waitForTimeout(s.post || 2500);
      const file = path.join(dir, `${String(i).padStart(2, '0')}_${s.name}.png`);
      await page.screenshot({ path: file });
      await page.close();
      console.log('shot', d.tag, s.name);
    }
    await ctx.close();
  }
  await browser.close();
  console.log('all done ->', outRoot);
})().catch((e) => {
  console.error(e);
  process.exit(1);
});
