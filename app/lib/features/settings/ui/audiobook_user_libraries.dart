import 'package:cantinarr/l10n/l10n.dart';
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

  String name(BuildContext context, String id) =>
      libraries.where((l) => l.id == id).map((l) => l.name).firstOrNull ??
      context.l10n.settingsMessageUnknownLibraryValue1((id).toString());

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
                    context.l10n
                        .settingsEnableAccountManagementInSettingsUsersToChange,
                style: const TextStyle(
                    color: AppTheme.textSecondary, fontSize: 12))
          else ...[
            InputDecorator(
                decoration: InputDecoration(
                    labelText: context.l10n.settingsMessageLibrariesForValue1(
                        (username).toString()),
                    contentPadding: const EdgeInsets.symmetric(
                        horizontal: 12, vertical: 4)),
                child: DropdownButtonHideUnderline(
                    child: DropdownButton<String>(
                  value: mode,
                  isExpanded: true,
                  items: [
                    if (keepExisting)
                      DropdownMenuItem(
                          value: 'keep',
                          child: Text(
                              context.l10n.settingsKeepCurrentAbsLibraries)),
                    DropdownMenuItem(
                        value: 'default',
                        child: Text(context.l10n.settingsUseServerDefault)),
                    DropdownMenuItem(
                        value: 'all',
                        child: Text(context.l10n.settingsAllLibraries)),
                    DropdownMenuItem(
                        value: 'selected',
                        child: Text(context.l10n.settingsChooseLibraries)),
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
                Text(context
                    .l10n.settingsLoadThisServerSLibrariesToChooseAccess),
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
                      title: Text(name(context, id)),
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
                Text(context.l10n.settingsChooseAtLeastOneLibrary,
                    style: TextStyle(color: AppTheme.error)),
            ] else
              Text(
                  mode == 'keep'
                      ? context.l10n
                          .settingsTheAccountKeepsItsCurrentLibraryPermissions
                      : mode == 'all' || ids.isEmpty
                          ? context.l10n
                              .settingsAllLibrariesIncludingLibrariesAddedLater
                          : ids.map((id) => name(context, id)).join(', '),
                  style: const TextStyle(
                      color: AppTheme.textSecondary, fontSize: 12)),
          ],
          if (policy.syncPending)
            Padding(
                padding: EdgeInsets.only(top: 6),
                child: Text(
                    context.l10n.settingsLibraryChangePendingCantinarrWillRetry,
                    style: TextStyle(
                        color: AppTheme.textSecondary, fontSize: 12))),
        ]));
  }
}
