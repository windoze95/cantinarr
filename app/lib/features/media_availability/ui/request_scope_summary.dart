import 'package:flutter/material.dart';
import '../../../l10n/l10n.dart';
import '../data/request_episode_scope.dart';

/// Reviews the saved initial scope. Later following and availability changes
/// must not rewrite what the administrator is approving or previously approved.
class RequestScopeSummary extends StatelessWidget {
  final RequestEpisodeScope scope;
  const RequestScopeSummary({super.key, required this.scope});

  @override
  Widget build(BuildContext context) {
    final l = context.l10n;
    return Padding(
        padding: const EdgeInsets.only(top: 12),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
                l.availabilityMissingEpisodeScope(scope.missingEpisodes.length),
                style: const TextStyle(fontWeight: FontWeight.w600)),
            ..._seasonLines(context, scope.missingEpisodes),
            const SizedBox(height: 8),
            Text(
                l.availabilitySatisfiedEpisodeScope(
                    scope.initiallySatisfiedEpisodes.length),
                style: const TextStyle(fontWeight: FontWeight.w600)),
            ..._seasonLines(context, scope.initiallySatisfiedEpisodes),
          ],
        ));
  }

  List<Widget> _seasonLines(
      BuildContext context, List<RequestScopeEpisode> episodes) {
    final bySeason = <int, List<int>>{};
    for (final episode in episodes) {
      bySeason
          .putIfAbsent(episode.seasonNumber, () => [])
          .add(episode.episodeNumber);
    }
    return [
      for (final season in bySeason.keys.toList()..sort())
        Text(
            context.l10n.availabilityEpisodeScopeSeason(
                season, _ranges(bySeason[season]!)),
            style: const TextStyle(fontSize: 13)),
    ];
  }

  String _ranges(List<int> values) {
    final numbers = values.toSet().toList()..sort();
    final ranges = <String>[];
    for (var i = 0; i < numbers.length; i++) {
      final start = numbers[i];
      var end = start;
      while (i + 1 < numbers.length && numbers[i + 1] == end + 1) {
        end = numbers[++i];
      }
      ranges.add(start == end ? '$start' : '$start-$end');
    }
    return ranges.join(', ');
  }
}
