import '../../discover/data/tmdb_models.dart';

/// Composes a season row's display label: its title, plus a first-air year
/// when TMDB actually knows one.
///
/// Absence is rendered as silence, not as a placeholder (D-03). TMDB sends
/// `""` for an unaired or otherwise-unknown season air date -- that is the
/// common case, not an edge case -- so an unusable `airDate` yields exactly
/// the bare title, with no separator, no blank, no dash, and no "TBA". A
/// year is shown rather than a full date because a year is the disambiguator
/// a requester actually needs when picking between seasons of a long-running
/// series (D-02); nothing here reads the system clock, so a future year
/// renders the same as a past one (D-05).
const String _separator = ' · ';

/// Builds the label shown on a season row: `"Season 1 · 2019"`, or just
/// `"Season 1"` when the air date is unknown or unusable.
String seasonRowLabel(Season season) {
  final rawName = season.name?.trim();
  final title = (rawName != null && rawName.isNotEmpty)
      ? rawName
      : 'Season ${season.seasonNumber}';

  final year = _resolveYear(season.airDate);
  return year == null ? title : '$title$_separator$year';
}

/// Returns the 4-digit leading year of [airDate] when it is present and
/// numeric, else null. Never echoes any part of the raw string other than
/// those validated digits.
String? _resolveYear(String? airDate) {
  final trimmed = airDate?.trim();
  if (trimmed == null || trimmed.length < 4) return null;
  final candidate = trimmed.substring(0, 4);
  return int.tryParse(candidate) == null ? null : candidate;
}
