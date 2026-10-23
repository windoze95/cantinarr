import 'package:intl/intl.dart';
import 'package:cantinarr/l10n/l10n.dart';
import '../../discover/data/tmdb_models.dart';
import 'release_schedule.dart';

/// UTC is only a container for calendar components here, never a conversion
/// of an air time. It also avoids daylight-saving gaps at local midnight.
DateTime tvCalendarDay(DateTime date) =>
    DateTime.utc(date.year, date.month, date.day);

/// TMDB TV dates must be complete, real YYYY-MM-DD calendar dates.
/// DateTime.parse alone accepts year-only values and normalizes bad days.
DateTime? parseTVDate(String? value) {
  if (value == null ||
      value.length != 10 ||
      !RegExp(r'^[0-9]{4}-[0-9]{2}-[0-9]{2}$').hasMatch(value)) {
    return null;
  }
  final year = int.parse(value.substring(0, 4));
  final month = int.parse(value.substring(5, 7));
  final day = int.parse(value.substring(8, 10));
  if (year < 1) return null;
  final date = DateTime.utc(year, month, day);
  return date.year == year && date.month == month && date.day == day
      ? date
      : null;
}

/// The original series premiere, using Season 1 only when first_air_date
/// is absent or invalid. A valid past date must never be replaced by a
/// returning season's future date, and Specials are not a series premiere.
DateTime? tvPremiereDate(TVDetail detail) {
  final first = parseTVDate(detail.firstAirDate);
  if (first != null) return first;
  for (final season in detail.seasons) {
    if (season.seasonNumber != 1) continue;
    final date = parseTVDate(season.airDate);
    if (date != null) return date;
  }
  return null;
}

/// One upcoming date beneath the status, independent of library availability.
/// Call only with successfully loaded metadata: TBA describes an expected
/// event with no usable date, never a failed metadata request.
String? upcomingTVDateLabel(TVDetail detail,
    {DateTime? now, AppLocalizations? strings}) {
  final today = tvCalendarDay(now ?? DateTime.now());
  final premiere = tvPremiereDate(detail);
  if (premiere != null && !premiere.isBefore(today)) {
    final date = _dateLabel(premiere, today, strings);
    return strings?.detailTVPremieres(date) ?? 'Premieres $date';
  }

  final next = detail.nextEpisodeToAir;
  final nextDate = parseTVDate(next?.airDate);
  if (next != null && nextDate != null && !nextDate.isBefore(today)) {
    final season = next.seasonNumber;
    final episode = next.episodeNumber;
    if (season != null && season > 0 && episode == 1) {
      return _premiereDateLabel(season, nextDate, today, strings);
    }
    final numbers =
        season != null && season >= 0 && episode != null && episode > 0
            ? ' S$season E$episode'
            : '';
    final date = _dateLabel(nextDate, today, strings);
    return strings?.detailTVNextEpisode(numbers, date) ??
        'Next episode$numbers · $date';
  }

  // TMDB may announce a season before filling next_episode_to_air. Pick the
  // earliest real season by date, regardless of the order of the payload.
  final seasons = <({int number, DateTime date})>[
    for (final season in detail.seasons)
      if (season.seasonNumber > 0)
        if (parseTVDate(season.airDate) case final date?)
          if (!date.isBefore(today)) (number: season.seasonNumber, date: date),
  ]..sort((a, b) {
      final byDate = a.date.compareTo(b.date);
      return byDate != 0 ? byDate : a.number.compareTo(b.number);
    });
  if (seasons.isNotEmpty) {
    final season = seasons.first;
    return _premiereDateLabel(season.number, season.date, today, strings);
  }

  final status = detail.status?.trim().toLowerCase();
  if (status == 'ended' || status == 'canceled' || status == 'cancelled') {
    return null;
  }
  final hasAired = (premiere != null && premiere.isBefore(today)) ||
      detail.lastEpisodeToAir != null ||
      (nextDate != null && nextDate.isBefore(today)) ||
      detail.seasons.any((season) {
        final date = parseTVDate(season.airDate);
        return season.seasonNumber > 0 && date != null && date.isBefore(today);
      });
  if (!hasAired &&
      const {'planned', 'pilot', 'in production'}.contains(status)) {
    return strings?.detailTVPremiereTBA ?? 'Premiere date TBA';
  }
  // A stale, past next-episode date is not evidence of another episode.
  if (status == 'returning series' || (next != null && nextDate == null)) {
    return strings?.detailTVNextEpisodeTBA ?? 'Next episode date TBA';
  }
  return null;
}

String _premiereDateLabel(
    int season, DateTime date, DateTime today, AppLocalizations? strings) {
  final label = _dateLabel(date, today, strings);
  if (season == 1)
    return strings?.detailTVPremieres(label) ?? 'Premieres $label';
  return strings?.detailTVSeasonPremieres(season, label) ??
      'Season $season premieres $label';
}

String _dateLabel(DateTime date, DateTime today, AppLocalizations? strings) {
  if (date == today) return strings?.browseToday ?? 'today';
  return strings == null
      ? formatReleaseDate(date)
      : DateFormat.yMMMd(strings.dateLocale).format(date);
}
