// Exact cover sources for the ListenBrainz 2026-09-26 snapshot in
// test/preview/screenshot_music.dart. Cover art remains its owners' work;
// these are real catalog covers, not the demo's CC0 album assets.
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const sources = {
  "21159e3f-172e-43f6-aa7d-8e06a81fea49": "https://coverartarchive.org/release/9643849e-1d87-49eb-bbf0-53336942d1b5/44649457301-500.jpg",
  "4c87e1ec-5d76-4f74-a683-2177e5f0ddf1": "https://coverartarchive.org/release/9a0acf89-d331-4ea0-a8b0-b20b6b3dd8ee/35452906276-500.jpg",
  "e51c54ea-71d2-413a-9fbe-1be9389b0fa6": "https://coverartarchive.org/release/183b9a00-3169-402a-835a-dca1918906de/37167600440-500.jpg",
  "41ab9590-7399-4397-883d-3f9ed2d8df60": "https://coverartarchive.org/release/b91e409c-46d4-4828-9176-4ae54a4bf9f4/45600783094-500.jpg",
  "62f46692-ce41-4474-b34e-946516fa55dd": "https://coverartarchive.org/release/21282f34-eb18-402b-b9e7-e125a6c94c51/41985751334-500.jpg",
  "5e23f1d2-6055-4b29-9aa5-9578debf5960": "https://coverartarchive.org/release/a224c449-2816-4c8d-b85f-d23bc9d24ef3/43379051269-500.jpg",
  "917b99b5-1c2c-409c-8b3d-4c97b7abaede": "https://coverartarchive.org/release/4741120f-c179-4b72-a39d-f6106b74c334/26296861263-500.jpg",
  "728f288b-eb1c-4ce1-98a1-d4124c897aaf": "https://coverartarchive.org/release/bdf973ab-5a6b-452c-91c5-df0706526d71/25478145851-500.jpg",
  "8a410a90-dca8-46b5-97d7-b31e62bb040a": "https://coverartarchive.org/release/978155a5-34c8-4f3b-bb14-934e2d845d55/46111435378-500.jpg",
  "93a4d718-6708-47bd-8a51-c2012ed4552e": "https://coverartarchive.org/release/eac55d5e-cef8-427e-a1f2-f6e3fc41223b/46199652584-500.jpg",
  "f1081d87-40a7-4004-8a3e-e7809961ceac": "https://coverartarchive.org/release/882d093d-3e98-4a4e-a5e6-052eb3e84c73/45324293715-500.jpg",
  "aecfecc7-4d43-45af-b028-0c484f8a830d": "https://coverartarchive.org/release/a5240205-43ad-49d5-b21e-2c29b465d835/46288991555-500.jpg",
  "12ad45f6-32d9-409d-8f35-8e7761b54607": "https://coverartarchive.org/release/51dfc747-054e-4a5e-a8f3-210b855f7fd8/44849457382-500.jpg",
  "9f22a118-9e5c-44ec-97aa-565f891c6afc": "https://coverartarchive.org/release/5444766c-224a-4e66-88c2-c1cabb58c1fd/46293622125-500.jpg",
  "cfce0bec-497d-4912-b265-849eb1b609a4": "https://coverartarchive.org/release/6000d127-4204-4405-9dc5-a7729341e1fb/46288788694-500.jpg",
  "e14c8027-5563-4764-9d67-7636076b8f1c": "https://coverartarchive.org/release/d4830020-dca7-4a6a-b02e-412b77ccb095/46293621895-500.jpg"
};
const cache = path.join(os.tmpdir(), 'cantinarr-store-music-artwork');
let artwork;

module.exports = async (page) => {
  if (!artwork) {
    fs.mkdirSync(cache, { recursive: true });
    artwork = new Map(await Promise.all(Object.entries(sources).map(async ([id, url]) => {
      const file = path.join(cache, id);
      if (!fs.existsSync(file)) {
        const response = await fetch(url, { signal: AbortSignal.timeout(45000) });
        if (!response.ok) throw new Error(`Music artwork ${id}: HTTP ${response.status}`);
        fs.writeFileSync(file, Buffer.from(await response.arrayBuffer()));
      }
      return [id, fs.readFileSync(file)];
    })));
  }
  await page.route('**/api/discover/music/artwork/*', async route => {
    const id = new URL(route.request().url()).pathname.split('/').pop();
    const body = artwork.get(id);
    if (!body) throw new Error(`Missing music artwork: ${id}`);
    await route.fulfill({
      status: 200,
      contentType: body[0] === 0x89 ? 'image/png' : 'image/jpeg',
      headers: { 'access-control-allow-origin': '*' },
      body,
    });
  });
};
