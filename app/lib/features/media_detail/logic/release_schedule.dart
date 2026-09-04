import 'package:intl/intl.dart';

import '../../discover/data/tmdb_models.dart';

/// TMDB release-date types this app shows, mapped to requester vocabulary
/// (never arr or TMDB jargon). Type 1 (Premiere, the festival circuit) and
/// type 6 (TV) are deliberately absent from this map: they answer a question
/// nobody asks a movie detail page ("when did it premiere at a festival?",
/// "when did it air on TV?") and would just be noise next to the milestones
/// that actually matter to someone deciding whether to watch.
const Map<int, String> _typeLabels = {
  2: 'In cinemas (limited)',
  3: 'In cinemas',
  4: 'Digital',
  5: 'Blu-ray / DVD',
};

/// One release milestone TMDB knows about, already filtered and labeled for
/// display.
class ReleaseMilestone {
  /// TMDB release-date type code (a key of [_typeLabels]). Carried alongside
  /// the label so presentation can branch on the milestone's identity rather
  /// than string-matching display copy, which would break silently the first
  /// time a label is reworded.
  final int type;

  /// Requester-facing label, e.g. "In cinemas" / "Digital".
  final String label;

  /// The calendar date it lands on (local midnight; see
  /// [TmdbReleaseDateEntry] for why this is never parsed as an instant).
  final DateTime date;

  /// True when [date] is today or later relative to the `now` the schedule
  /// was resolved against; false once it has passed. Both are shown — a
  /// milestone already in the past is still reference information, just
  /// rendered muted instead of accented.
  final bool isUpcoming;

  const ReleaseMilestone({
    required this.type,
    required this.label,
    required this.date,
    required this.isUpcoming,
  });
}

/// The resolved release schedule for one movie: which region's dates are
/// being shown, and the milestones themselves, sorted soonest first.
class ReleaseSchedule {
  /// ISO-3166-1 country code the milestones below belong to, rendered in the
  /// section header so the reader knows what was actually answered rather
  /// than reading the list as universal truth.
  final String regionCode;

  final List<ReleaseMilestone> milestones;

  const ReleaseSchedule({required this.regionCode, required this.milestones});
}

DateTime _dateOnly(DateTime d) => DateTime(d.year, d.month, d.day);

/// Builds the milestone list for one region's raw TMDB entries, applying the
/// type filter and label map, dropping unknown-date entries, and collapsing
/// same-type duplicates to the earliest date. Returns an empty list when the
/// region has no milestone this app shows — including a region carrying only
/// a Premiere or TV entry.
List<ReleaseMilestone> _milestonesFor(
  List<TmdbReleaseDateEntry> entries,
  DateTime today,
) {
  // Earliest-wins per type: track the best (earliest) date seen for each
  // shown type before turning survivors into milestones. This means two
  // theatrical entries (e.g. two different release notes on the same date
  // range) never produce two "In cinemas" rows.
  final earliestByType = <int, DateTime>{};
  for (final entry in entries) {
    final label = _typeLabels[entry.type];
    final date = entry.date;
    if (label == null || date == null) continue;
    final existing = earliestByType[entry.type];
    if (existing == null || date.isBefore(existing)) {
      earliestByType[entry.type] = date;
    }
  }

  final milestones = earliestByType.entries
      .map((e) {
        final date = _dateOnly(e.value);
        return ReleaseMilestone(
          type: e.key,
          label: _typeLabels[e.key]!,
          date: date,
          isUpcoming: !date.isBefore(today),
        );
      })
      .toList();

  // Sort by date ascending; tie-break on TMDB type ascending so a limited
  // run (2) reads before a wide one (3) on a shared day, and a cinema date
  // (2/3) reads before a same-day digital one (4).
  milestones.sort((a, b) {
    final byDate = a.date.compareTo(b.date);
    if (byDate != 0) return byDate;
    return a.type.compareTo(b.type);
  });

  return milestones;
}

/// Resolves the release schedule to show for a movie, given TMDB's
/// per-region `release_dates.results` list.
///
/// Region resolution order (D-06): [preferredRegion] (the viewer's locale
/// country code) first, then `US` (matching the server's default region and
/// the client's `language=en-US`), then the first region in [regions] that
/// carries at least one shown milestone. A region is skipped — not
/// selected-and-left-empty — when none of its entries survive the type
/// filter, so a region whose only entry is a Premiere never wins and leaves
/// the section looking broken.
///
/// Returns null when [regions] is empty, or when no region anywhere in the
/// payload has a milestone this app shows — the whole section is omitted in
/// that case rather than rendered empty, per the project's
/// absence-vs-blindness rule.
ReleaseSchedule? resolveReleaseSchedule(
  List<TmdbReleaseDateRegion> regions, {
  String? preferredRegion,
  DateTime? now,
}) {
  if (regions.isEmpty) return null;
  final today = _dateOnly(now ?? DateTime.now());

  final byCode = <String, TmdbReleaseDateRegion>{
    for (final r in regions) r.countryCode: r,
  };

  ReleaseSchedule? tryRegion(String? code) {
    if (code == null) return null;
    final region = byCode[code];
    if (region == null) return null;
    final milestones = _milestonesFor(region.entries, today);
    if (milestones.isEmpty) return null;
    return ReleaseSchedule(regionCode: code, milestones: milestones);
  }

  final preferred = tryRegion(preferredRegion);
  if (preferred != null) return preferred;

  final us = tryRegion('US');
  if (us != null) return us;

  for (final region in regions) {
    final milestones = _milestonesFor(region.entries, today);
    if (milestones.isNotEmpty) {
      return ReleaseSchedule(
        regionCode: region.countryCode,
        milestones: milestones,
      );
    }
  }

  return null;
}

/// Renders a release date as `MMM d, yyyy`, year always included. This
/// differs deliberately from `release_window.dart`'s formatter, which drops
/// the current year: that line only ever shows near-future dates, while this
/// section can show dates spanning decades, where a bare "Sep 23" would be
/// ambiguous about which year.
String formatReleaseDate(DateTime date) => DateFormat('MMM d, yyyy').format(date);
