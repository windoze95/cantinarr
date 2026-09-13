/// Synthetic TMDB detail fixtures; all schedule tests use a fixed clock.
const carrieTVFixture = <String, dynamic>{
  'id': 123,
  'name': 'Carrie',
  'status': 'In Production',
  'first_air_date': '2026-10-07',
  'number_of_seasons': 1,
  'number_of_episodes': 8,
  'next_episode_to_air': null,
  'last_episode_to_air': null,
  'seasons': [
    {
      'id': 1,
      'season_number': 1,
      'name': 'Season 1',
      'episode_count': 8,
      'air_date': '2026-10-07',
    },
  ],
};

const returningTVFixture = <String, dynamic>{
  'id': 123,
  'name': 'Returning Show',
  'status': 'Returning Series',
  'first_air_date': '2024-10-02',
  'last_episode_to_air': {
    'id': 11,
    'season_number': 1,
    'episode_number': 8,
    'air_date': '2024-11-20',
  },
  'next_episode_to_air': {
    'id': 12,
    'season_number': 2,
    'episode_number': 3,
    'air_date': '2026-10-14',
  },
};
