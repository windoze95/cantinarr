import 'package:flutter/widgets.dart';

import 'l10n.dart';

/// Translates app-owned display labels. Pass only application vocabulary here;
/// instance names, media titles, profiles, and other user text stay untouched.
String localizeShellLabel(BuildContext context, String label) =>
    switch (label) {
      'Discover' => context.l10n.commonLabelDiscover,
      'Downloads' => context.l10n.commonLabelDownloads,
      'Monitoring' => context.l10n.commonLabelMonitoring,
      'Transcoding' => context.l10n.commonLabelTranscoding,
      'AI Assistant' => context.l10n.commonLabelAIAssistant,
      'Movies' => context.l10n.commonLabelMovies,
      'TV Shows' => context.l10n.commonLabelTVShows,
      'Releases' => context.l10n.commonLabelReleases,
      'Books' => context.l10n.commonLabelBooks,
      'Music' => context.l10n.commonLabelMusic,
      'Library' => context.l10n.commonLabelLibrary,
      'Queue' => context.l10n.commonLabelQueue,
      'History' => context.l10n.commonLabelHistory,
      'Wanted' => context.l10n.commonLabelWanted,
      'Calendar' => context.l10n.commonLabelCalendar,
      'Activity' => context.l10n.commonLabelActivity,
      'Stats' => context.l10n.commonLabelStats,
      'Statistics' => context.l10n.commonLabelStatistics,
      'Movies & TV' => context.l10n.commonLabelMoviesTV,
      'Watch' => context.l10n.commonLabelWatch,
      'Listen' => context.l10n.commonLabelListen,
      'Watch & listen' => context.l10n.commonLabelWatchlisten,
      'Media access' => context.l10n.commonLabelMediaaccess,
      'First Name' => context.l10n.commonLabelFirstName,
      'Alphabetical' => context.l10n.commonLabelAlphabetical,
      'Last Name' => context.l10n.commonLabelLastName,
      'Date Added' => context.l10n.commonLabelDateAdded,
      'Certification' => context.l10n.commonLabelCertification,
      'Digital Release' => context.l10n.commonLabelDigitalRelease,
      'IMDb Rating' => context.l10n.commonLabelIMDbRating,
      'In Cinemas' => context.l10n.commonLabelInCinemas,
      'Minimum Availability' => context.l10n.commonLabelMinimumAvailability,
      'Status' => context.l10n.commonLabelStatus,
      'Monitored Status' => context.l10n.commonLabelMonitoredStatus,
      'Original Language' => context.l10n.commonLabelOriginalLanguage,
      'Original Title' => context.l10n.commonLabelOriginalTitle,
      'Path' => context.l10n.commonLabelPath,
      'Physical Release' => context.l10n.commonLabelPhysicalRelease,
      'Popularity' => context.l10n.commonLabelPopularity,
      'Quality Profile' => context.l10n.commonLabelQualityProfile,
      'Release Date' => context.l10n.commonLabelReleaseDate,
      'Rotten Tomatoes Rating' => context.l10n.commonLabelRottenTomatoesRating,
      'Runtime' => context.l10n.commonLabelRuntime,
      'Size' => context.l10n.commonLabelSize,
      'Studio' => context.l10n.commonLabelStudio,
      'Tags' => context.l10n.commonLabelTags,
      'TMDB Rating' => context.l10n.commonLabelTMDBRating,
      'Trakt Rating' => context.l10n.commonLabelTraktRating,
      'Year' => context.l10n.commonLabelYear,
      'Episode Completion' => context.l10n.commonLabelEpisodeCompletion,
      'Episode Count' => context.l10n.commonLabelEpisodeCount,
      'Latest Season' => context.l10n.commonLabelLatestSeason,
      'Network' => context.l10n.commonLabelNetwork,
      'Next Airing' => context.l10n.commonLabelNextAiring,
      'Previous Airing' => context.l10n.commonLabelPreviousAiring,
      'Rating' => context.l10n.commonLabelRating,
      'Seasons' => context.l10n.commonLabelSeasons,
      'Type' => context.l10n.commonLabelType,
      'Audiobook Metadata Profile' =>
        context.l10n.commonLabelAudiobookMetadataProfile,
      'Audiobook Quality Profile' =>
        context.l10n.commonLabelAudiobookQualityProfile,
      'Book Completion' => context.l10n.commonLabelBookCompletion,
      'Albums' => context.l10n.commonLabelAlbums,
      'eBook Metadata Profile' => context.l10n.commonLabeleBookMetadataProfile,
      'eBook Quality Profile' => context.l10n.commonLabeleBookQualityProfile,
      'Last Book' => context.l10n.commonLabelLastBook,
      'Next Book' => context.l10n.commonLabelNextBook,
      'Last Album' => context.l10n.commonLabelLastAlbum,
      'Metadata Profile' => context.l10n.commonLabelMetadataProfile,
      'Next Album' => context.l10n.commonLabelNextAlbum,
      'Track Completion' => context.l10n.commonLabelTrackCompletion,
      'Track Count' => context.l10n.commonLabelTrackCount,
      'Spotlight' => context.l10n.commonLabelSpotlight,
      'Movie' => context.l10n.commonLabelMovie,
      'TV Show' => context.l10n.commonLabelTVShow,
      'Book' => context.l10n.commonLabelBook,
      'Album' => context.l10n.commonLabelAlbum,
      'Artist' => context.l10n.commonLabelArtist,
      'Author' => context.l10n.commonLabelAuthor,
      'Automatic search' => context.l10n.commonLabelAutomaticsearch,
      'Interactive search' => context.l10n.commonLabelInteractivesearch,
      'Manage monitoring…' => context.l10n.commonLabelManagemonitoring,
      'Refresh metadata' => context.l10n.commonLabelRefreshmetadata,
      'Rescan files' => context.l10n.commonLabelRescanfiles,
      'Remove…' => context.l10n.commonLabelRemove,
      _ => label,
    };

/// Action factories have no widget context. Resolve their app-owned labels only
/// when they are displayed, preserving the public factory and user data.
String localizeLibraryAction(BuildContext context, String label) {
  if (context.l10n.localeName.startsWith('en')) return label;
  for (final action in ['Edit ', 'Unmonitor ', 'Monitor ']) {
    if (label.startsWith(action)) {
      final noun = localizeLibraryNoun(context, label.substring(action.length));
      return switch (action) {
        'Edit ' => context.l10n.commonEditNoun(noun),
        'Unmonitor ' => context.l10n.commonUnmonitorNoun(noun),
        _ => context.l10n.commonMonitorNoun(noun),
      };
    }
  }
  return localizeShellLabel(context, label);
}

String localizeLibraryNoun(BuildContext context, String noun) {
  if (context.l10n.localeName.startsWith('en')) return noun;
  return localizeShellLabel(
          context,
          switch (noun) {
            'author' => 'Author',
            'artist' => 'Artist',
            'book' => 'Book',
            'album' => 'Album',
            'movie' => 'Movie',
            'series' => 'TV Show',
            _ => noun,
          })
      .toLowerCase();
}
