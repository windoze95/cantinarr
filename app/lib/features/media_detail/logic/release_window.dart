import 'package:intl/intl.dart';

import '../../request/data/request_service.dart';

/// One release milestone that hasn't happened yet.
class PendingRelease {
  /// Requester-facing name for the milestone: "In cinemas" / "Digital". Never
  /// arr vocabulary.
  final String label;

  /// The calendar date it lands on (local midnight, never localised further).
  final DateTime date;

  const PendingRelease({required this.label, required this.date});
}

DateTime _dateOnly(DateTime d) => DateTime(d.year, d.month, d.day);

/// The release milestones still ahead of [now], soonest first.
///
/// The "collapse" behaviour asked for on the detail page falls out of this
/// rather than being special-cased: before the theatrical date both milestones
/// are ahead and both show; once it passes only the digital one is left; once
/// that passes the list is empty and the page says nothing. Odd shapes handle
/// themselves too — a day-and-date release, a title with only one known date,
/// or a digital date that precedes the theatrical one.
///
/// A date landing exactly on [now] still counts as pending, matching how the
/// dashboard releases timeline treats "today", and renders as "today" rather
/// than as a date the viewer has to compare against a calendar.
///
/// Returns nothing when [status] is available: the title is watchable now, so
/// when it reached cinemas answers a question nobody on this screen is asking.
/// (A file can land before the digital date — an early web release — so this
/// is a real case, not a theoretical one.)
List<PendingRelease> pendingReleases(
  MovieReleaseDates releases, {
  required RequestStatus status,
  DateTime? now,
}) {
  if (status == RequestStatus.available) return const [];
  final today = _dateOnly(now ?? DateTime.now());
  final out = <PendingRelease>[
    if (releases.inCinemas != null)
      PendingRelease(label: 'In cinemas', date: _dateOnly(releases.inCinemas!)),
    if (releases.digital != null)
      PendingRelease(label: 'Digital', date: _dateOnly(releases.digital!)),
  ]..removeWhere((r) => r.date.isBefore(today));
  out.sort((a, b) => a.date.compareTo(b.date));
  return out;
}

/// Renders [pending] as one line, e.g. "In cinemas Jun 12 • Digital Sep 4".
///
/// The year is dropped for dates in the current year, which is nearly all of
/// them, and kept when it genuinely disambiguates.
String formatPendingReleases(List<PendingRelease> pending, {DateTime? now}) {
  final today = _dateOnly(now ?? DateTime.now());
  return pending
      .map((r) => '${r.label} ${_formatDate(r.date, today)}')
      .join(' • ');
}

String _formatDate(DateTime date, DateTime today) {
  if (date == today) return 'today';
  final pattern = date.year == today.year ? 'MMM d' : 'MMM d, yyyy';
  return DateFormat(pattern).format(date);
}
