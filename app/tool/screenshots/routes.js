// Per-shot routes + interactions for shoot.js. Array order defines the
// numbering (and therefore the store display order) of the output files.
module.exports = [
  { name: 'dashboard_movies', route: '/dashboard/movies', settle: 6500 },
  {
    name: 'search',
    route: '/dashboard/movies',
    settle: 5500,
    post: 3500,
    // Focus the shell search bar and type; the results overlay appears after
    // the 400ms debounce. A short generic query reads naturally over the
    // fixed mixed result set the harness returns.
    actions: async (page, d) => {
      await page.mouse.click(d.vw / 2, 40);
      await page.waitForTimeout(400);
      await page.keyboard.type('the', { delay: 90 });
    },
  },
  { name: 'detail_movie', route: '/detail/movie/687163', settle: 6500 },
  { name: 'detail_tv', route: '/detail/tv/94997', settle: 6500 },
  { name: 'dashboard_tv', route: '/dashboard/tv', settle: 6500 },
  { name: 'releases', route: '/dashboard/releases', settle: 5500 },
  { name: 'downloads', route: '/downloads/queue', settle: 5500 },
  { name: 'sonarr_library', route: '/sonarr/library', settle: 5500 },
  { name: 'approvals', route: '/approvals', settle: 5500 },
];
