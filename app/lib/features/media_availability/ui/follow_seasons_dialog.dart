import 'package:flutter/material.dart';
import '../../../l10n/l10n.dart';
import '../../discover/data/tmdb_models.dart';

class FollowSeasonsDialog extends StatefulWidget {
  final List<Season> seasons;
  const FollowSeasonsDialog({super.key, required this.seasons});
  @override
  State<FollowSeasonsDialog> createState() => _FollowSeasonsDialogState();
}

class _FollowSeasonsDialogState extends State<FollowSeasonsDialog> {
  final Set<int> _selected = {};
  @override
  Widget build(BuildContext context) {
    final l = context.l10n;
    return AlertDialog(
        title: Text(l.availabilityFollowSeasons),
        content: SizedBox(
            width: 440,
            child: SingleChildScrollView(
                child: Column(
                    mainAxisSize: MainAxisSize.min,
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                  Text(l.availabilityFollowExplanation),
                  for (final season
                      in widget.seasons.where((s) => s.seasonNumber > 0))
                    CheckboxListTile(
                        contentPadding: EdgeInsets.zero,
                        title: Text(season.name ?? '${season.seasonNumber}'),
                        value: _selected.contains(season.seasonNumber),
                        onChanged: (checked) => setState(() {
                              if (checked == true) {
                                _selected.add(season.seasonNumber);
                              } else {
                                _selected.remove(season.seasonNumber);
                              }
                            })),
                ]))),
        actions: [
          TextButton(
              onPressed: () => Navigator.pop(context),
              child: Text(l.settingsCancel)),
          FilledButton(
              onPressed: _selected.isEmpty
                  ? null
                  : () => Navigator.pop(context, _selected.toList()..sort()),
              child: Text(l.availabilityFollowSeasons))
        ]);
  }
}
