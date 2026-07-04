import '../data/sonarr_models.dart';

/// Episode ids the "Undownloaded" quick-select targets: episodes that have
/// aired but have no file. Unaired and TBA episodes are skipped — searching
/// for them finds nothing (an early release that did land already has a file
/// and is excluded as downloaded).
List<int> undownloadedEpisodeIds(List<SonarrEpisode> episodes) => [
      for (final e in episodes)
        if (!e.hasFile && e.hasAired) e.id,
    ];
