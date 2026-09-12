import 'package:flutter/material.dart';

import '../../../core/theme/app_theme.dart';
import '../data/audiobook_library_access.dart';
import '../data/instance_api_service.dart';

/// One person's policy within an ABS server, independent of their account.
class AudiobookUserLibraries extends StatelessWidget {
  final String username;
  final AudiobookLibraryPolicy policy;
  final List<MediaServerLibrary> libraries;
  final Set<String> defaultLibraryIds;
  final bool keepExisting;
  final bool enabled;
  final String? unavailableReason;
  final ValueChanged<AudiobookLibraryPolicy> onChanged;
  const AudiobookUserLibraries(
      {super.key,
      required this.username,
      required this.policy,
      required this.libraries,
      required this.defaultLibraryIds,
      required this.onChanged,
      this.keepExisting = false,
      this.enabled = true,
      this.unavailableReason});

  String name(String id) =>
      libraries.where((l) => l.id == id).map((l) => l.name).firstOrNull ??
      'Unknown library ($id)';

  @override
  Widget build(BuildContext context) {
    final mode = keepExisting ? 'keep' : policy.mode;
    final ids =
        mode == 'default' ? defaultLibraryIds : policy.libraryIds.toSet();
    return Padding(
        padding: const EdgeInsets.fromLTRB(40, 0, 0, 12),
        child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
          if (!enabled)
            Text(
                unavailableReason ??
                    'Enable account management in Settings → Users to change libraries here.',
                style: const TextStyle(
                    color: AppTheme.textSecondary, fontSize: 12))
          else ...[
            InputDecorator(
                decoration: InputDecoration(
                    labelText: 'Libraries for $username',
                    contentPadding: const EdgeInsets.symmetric(
                        horizontal: 12, vertical: 4)),
                child: DropdownButtonHideUnderline(
                    child: DropdownButton<String>(
                  value: mode,
                  isExpanded: true,
                  items: [
                    if (keepExisting)
                      const DropdownMenuItem(
                          value: 'keep',
                          child: Text('Keep current ABS libraries')),
                    const DropdownMenuItem(
                        value: 'default', child: Text('Use server default')),
                    const DropdownMenuItem(
                        value: 'all', child: Text('All libraries')),
                    const DropdownMenuItem(
                        value: 'selected', child: Text('Choose libraries')),
                  ],
                  onChanged: (value) {
                    if (value == null || value == 'keep') return;
                    onChanged(AudiobookLibraryPolicy(
                        mode: value,
                        libraryIds: value == 'selected'
                            ? policy.libraryIds
                            : const []));
                  },
                ))),
            const SizedBox(height: 6),
            if (mode == 'selected') ...[
              if (libraries.isEmpty)
                const Text('Load this server’s libraries to choose access.'),
              for (final id in {
                ...libraries.map((l) => l.id),
                ...policy.libraryIds
              })
                Material(
                    type: MaterialType.transparency,
                    child: CheckboxListTile(
                      contentPadding: EdgeInsets.zero,
                      dense: true,
                      controlAffinity: ListTileControlAffinity.leading,
                      title: Text(name(id)),
                      value: policy.libraryIds.contains(id),
                      onChanged: (checked) {
                        final next = policy.libraryIds.toSet();
                        checked == true ? next.add(id) : next.remove(id);
                        onChanged(AudiobookLibraryPolicy(
                            mode: 'selected',
                            libraryIds: next.toList()..sort()));
                      },
                    )),
              if (policy.libraryIds.isEmpty)
                const Text('Choose at least one library.',
                    style: TextStyle(color: AppTheme.error)),
            ] else
              Text(
                  mode == 'keep'
                      ? 'The account keeps its current library permissions.'
                      : mode == 'all' || ids.isEmpty
                          ? 'All libraries, including libraries added later.'
                          : ids.map(name).join(', '),
                  style: const TextStyle(
                      color: AppTheme.textSecondary, fontSize: 12)),
          ],
          if (policy.syncPending)
            const Padding(
                padding: EdgeInsets.only(top: 6),
                child: Text('Library change pending. Cantinarr will retry.',
                    style: TextStyle(
                        color: AppTheme.textSecondary, fontSize: 12))),
        ]));
  }
}
