// Per-shot routes + interactions for shoot.js. Array order defines the
// numbering (and therefore the store display order) of the output files.
module.exports = [
  { name: 'discover', route: '/dashboard/movies', settle: 6500 },
  {
    name: 'seasons',
    route: '/detail/tv/94997',
    settle: 6500,
    post: 1500,
    // The detail header grew tall enough to push the Seasons section below
    // the fold; scroll it into frame so the shot shows its subject.
    actions: async (page, d) => {
      await page.mouse.move(d.vw / 2, d.vh / 2);
      await page.mouse.wheel(0, Math.round(d.vh * 0.85));
    },
  },
  {
    name: 'search',
    route: '/dashboard/movies',
    settle: 5500,
    post: 3500,
    // Focus the shell search bar and type; the results overlay appears after
    // the 400ms debounce. A short generic query reads naturally over the
    // fixed mixed result set the harness returns.
    actions: async (page, d) => {
      // The field stays horizontally centered in both the mobile top bar and
      // desktop shell. Forty pixels is inside the input at every capture size.
      await page.mouse.click(d.vw / 2, 40);
      await page.waitForTimeout(400);
      await page.keyboard.type('the', { delay: 90 });
    },
  },
  {
    name: 'books',
    route: '/dashboard/books',
    // Three independent library reads (recent imports, authors, series) plus
    // Open Library cover art, so this one needs longer than a discover row.
    settle: 7500,
    post: 4000,
  },
  {
    name: 'movie_detail', route: '/detail/movie/687163', settle: 6500,
    skip: ['android', 'tablet10'],
  },
  {
    // Play slot 5 keeps the original TV library image, formerly slot 8.
    name: 'tv_library', route: '/sonarr/library', settle: 5500,
    skip: ['iphone69', 'ipad13'],
  },
  { name: 'releases', route: '/dashboard/releases', settle: 5500 },
  { name: 'downloads', route: '/downloads/queue', settle: 5500 },
  {
    name: 'tv_library', route: '/sonarr/library', settle: 5500,
    skip: ['android', 'tablet10'],
  },
  {
    // Music is slot 9 on Apple and slot 8 on Play. All earlier shots keep
    // their original service profile without Music in the navigation.
    name: 'music',
    route: '/dashboard/music',
    settle: 6500,
    setup: require('./music.js'),
  },
  {
    name: 'approvals',
    route: '/approvals',
    settle: 5500,
    // The App Store's tenth slot; Play is full at eight.
    skip: ['android', 'tablet10'],
  },
];
