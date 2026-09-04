import '../../discover/data/tmdb_models.dart';

/// One line of a title page's Details section: a short label and the value
/// TMDB knows for it. Lines exist only for what is known, so a title with
/// nothing known has no lines and the section does not render.
class TitleFact {
  final String label;
  final String value;

  const TitleFact(this.label, this.value);

  @override
  bool operator ==(Object other) =>
      other is TitleFact && other.label == label && other.value == value;

  @override
  int get hashCode => Object.hash(label, value);

  @override
  String toString() => 'TitleFact($label: $value)';
}

/// The crew jobs that count as "Written by". TMDB files a writing credit
/// under one of these; the rest of the Writing department (novel, characters,
/// comic book) is left to the full sheet.
const _writingJobs = {'Screenplay', 'Writer', 'Story', 'Teleplay'};

/// The Details lines for a movie, in reading order: a not-yet-released
/// status first because it qualifies everything under it, then who made it,
/// then where and how long, then the money.
List<TitleFact> movieFacts(MovieDetail detail) {
  final crew = detail.credits.crew;
  final directors = crew.where((c) => c.job == 'Director').map((c) => c.name);
  final writers =
      crew.where((c) => _writingJobs.contains(c.job)).map((c) => c.name);
  return [
    if (_unreleasedStatus(detail.status) case final status?)
      TitleFact('Status', status),
    if (joinNames(directors) case final names?) TitleFact('Directed by', names),
    if (joinNames(writers) case final names?) TitleFact('Written by', names),
    if (joinNames(detail.countries) case final names?)
      TitleFact('Country', names),
    if (formatRuntime(detail.runtime) case final runtime?)
      TitleFact('Runtime', runtime),
    if (formatMoney(detail.budget) case final budget?)
      TitleFact('Budget', budget),
    if (formatMoney(detail.revenue) case final revenue?)
      TitleFact('Revenue', revenue),
  ];
}

/// The Details lines for a show. Status always earns a line when TMDB has
/// one: whether a show is still returning, ended, or canceled is the first
/// thing a viewer wants to know about it.
List<TitleFact> tvFacts(TVDetail detail) => [
      if (_present(detail.status) case final status?)
        TitleFact('Status', status),
      if (joinNames(detail.createdBy.map((c) => c.name)) case final names?)
        TitleFact('Created by', names),
      if (joinNames(detail.networks.map((n) => n.name).whereType<String>())
          case final names?)
        TitleFact('Network', names),
      if (joinNames(detail.countries) case final names?)
        TitleFact('Country', names),
    ];

/// A movie status worth a line: anything TMDB says other than "Released",
/// which is the unremarkable case and would be noise on every title.
String? _unreleasedStatus(String? status) {
  final value = _present(status);
  return value == 'Released' ? null : value;
}

String? _present(String? value) {
  final trimmed = value?.trim() ?? '';
  return trimmed.isEmpty ? null : trimmed;
}

/// Up to [max] distinct non-blank names joined with commas, in the order
/// given, or null when there are none.
String? joinNames(Iterable<String> names, {int max = 3}) {
  final seen = <String>{};
  final kept = <String>[];
  for (final name in names) {
    final trimmed = name.trim();
    if (trimmed.isEmpty || !seen.add(trimmed)) continue;
    kept.add(trimmed);
    if (kept.length == max) break;
  }
  return kept.isEmpty ? null : kept.join(', ');
}

/// `2h 16m`, `2h`, or `58m`; null when unknown. TMDB stores 0 for a runtime
/// it does not have, which is unknown, not a zero-length film.
String? formatRuntime(int? minutes) {
  if (minutes == null || minutes <= 0) return null;
  final hours = minutes ~/ 60;
  final rest = minutes % 60;
  if (hours == 0) return '${rest}m';
  if (rest == 0) return '${hours}h';
  return '${hours}h ${rest}m';
}

const _moneyUnits = [(1000000000, 'B'), (1000000, 'M'), (1000, 'K')];

/// `$63M`, `$1.2B`, `$850K`: US dollars, which is what TMDB stores. One
/// decimal only while the leading figure is a single digit, so `$9.5M` but
/// `$464M`. Null below a thousand: TMDB holds 0 for unknown and junk like
/// `budget: 12`, and neither is a fact worth a line.
String? formatMoney(int? amount) {
  if (amount == null || amount < 1000) return null;
  for (var i = 0; i < _moneyUnits.length; i++) {
    final (size, suffix) = _moneyUnits[i];
    if (amount < size) continue;
    final value = amount / size;
    // 999,700,000 rounds to 1000M; say $1B instead.
    if (value >= 999.5 && i > 0) return '\$1${_moneyUnits[i - 1].$2}';
    var text = value < 10 ? value.toStringAsFixed(1) : '${value.round()}';
    if (text.endsWith('.0')) text = text.substring(0, text.length - 2);
    return '\$$text$suffix';
  }
  return null;
}

/// One department of the cast and crew sheet.
class CrewSection {
  final String department;
  final List<CrewMember> members;

  const CrewSection(this.department, this.members);
}

/// TMDB's departments in the order a viewer expects to read them. Anything
/// TMDB adds later sorts alphabetically after these.
const _departmentOrder = [
  'Creators',
  'Directing',
  'Writing',
  'Production',
  'Sound',
  'Camera',
  'Editing',
  'Art',
  'Costume & Make-Up',
  'Visual Effects',
  'Lighting',
  'Crew',
];

/// The crew grouped by department for the cast and crew sheet. Within a
/// department TMDB's order is kept, and a person credited with several jobs
/// there ("Producer" and "Executive Producer") gets one row naming them all.
List<CrewSection> crewSections(Iterable<CrewMember> crew) {
  final byDepartment = <String, List<CrewMember>>{};
  for (final member in crew) {
    final department =
        member.department.trim().isEmpty ? 'Crew' : member.department.trim();
    final rows = byDepartment.putIfAbsent(department, () => []);
    final index = rows.indexWhere((r) => r.id == member.id);
    if (index < 0) {
      rows.add(member);
    } else {
      final existing = rows[index];
      final jobs = existing.job.split(', ');
      if (!jobs.contains(member.job) && member.job.isNotEmpty) {
        rows[index] = CrewMember(
          id: existing.id,
          name: existing.name,
          job: existing.job.isEmpty ? member.job : '${existing.job}, ${member.job}',
          department: department,
          profilePath: existing.profilePath ?? member.profilePath,
        );
      }
    }
  }
  final known = _departmentOrder.where(byDepartment.containsKey);
  final rest = byDepartment.keys
      .where((d) => !_departmentOrder.contains(d))
      .toList()
    ..sort();
  return [
    for (final department in [...known, ...rest])
      CrewSection(department, byDepartment[department]!),
  ];
}
