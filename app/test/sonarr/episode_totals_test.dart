import 'package:cantinarr/features/sonarr/data/sonarr_models.dart';
import 'package:flutter_test/flutter_test.dart';

SonarrSeason _season(int number,
        {bool monitored = true, int files = 0, int episodes = 0, int total = 0}) =>
    SonarrSeason(
      seasonNumber: number,
      monitored: monitored,
      statistics: SonarrStatistics(
        episodeFileCount: files,
        episodeCount: episodes,
        totalEpisodeCount: total,
      ),
    );

void main() {
  group('SonarrSeries.episodeTotals', () {
    test('counts unmonitored seasons that percentComplete ignores', () {
      // One monitored, fully downloaded season; three unmonitored empty ones.
      // Sonarr's monitored-only statistics call this series 100% complete.
      final series = SonarrSeries(
        id: 1,
        title: 'Stranger Things',
        statistics: const SonarrStatistics(
          episodeFileCount: 9,
          episodeCount: 9,
          totalEpisodeCount: 34,
        ),
        seasons: [
          _season(1, monitored: false, episodes: 0, total: 8),
          _season(2, monitored: false, episodes: 0, total: 9),
          _season(3, monitored: false, episodes: 0, total: 8),
          _season(4, files: 9, episodes: 9, total: 9),
        ],
      );

      expect(series.percentComplete, 1.0);
      final (:files, :total) = series.episodeTotals;
      expect(files, 9);
      expect(total, 34);
    });

    test('excludes Specials and reads complete when all seasons have files',
        () {
      final series = SonarrSeries(
        id: 2,
        title: 'Done',
        seasons: [
          _season(0, files: 1, episodes: 2, total: 2),
          _season(1, files: 8, episodes: 8, total: 8),
        ],
      );

      final (:files, :total) = series.episodeTotals;
      expect(files, 8);
      expect(total, 8);
    });

    test('falls back to episodeCount, then series statistics', () {
      // Older payloads may omit totalEpisodeCount (0) or the whole seasons
      // statistics; the getter falls back rather than reporting 0/0.
      final perSeason = SonarrSeries(
        id: 3,
        title: 'No totals',
        seasons: [_season(1, files: 3, episodes: 6, total: 0)],
      );
      expect(perSeason.episodeTotals, (files: 3, total: 6));

      const seriesOnly = SonarrSeries(
        id: 4,
        title: 'No seasons',
        statistics: SonarrStatistics(
          episodeFileCount: 5,
          episodeCount: 10,
          totalEpisodeCount: 12,
        ),
      );
      expect(seriesOnly.episodeTotals, (files: 5, total: 12));
    });
  });
}
