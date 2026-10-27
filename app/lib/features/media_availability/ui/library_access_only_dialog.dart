import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/widgets/unsaved_changes_guard.dart';
import '../../../l10n/l10n.dart';
import '../data/media_availability_service.dart';

/// Opened only after an administrator explicitly chooses Library access only.
/// Draft choices never modify remote accounts or normal instance grants.
class LibraryAccessOnlyDialog extends ConsumerStatefulWidget {
  final int userId;
  final String initialProvider;
  const LibraryAccessOnlyDialog(
      {super.key, required this.userId, required this.initialProvider});
  @override
  ConsumerState<LibraryAccessOnlyDialog> createState() =>
      _LibraryAccessOnlyDialogState();
}

class _LibraryAccessOnlyDialogState
    extends ConsumerState<LibraryAccessOnlyDialog> {
  AvailabilityHouseholds? _data;
  Map<String, List<String>> _selected = {};
  late String _provider = widget.initialProvider;
  String? _error;
  bool _saving = false;
  final Set<String> _visitedProviders = {};
  MediaAvailabilityService get _service =>
      MediaAvailabilityService(ref.read(backendClientProvider));
  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final data = await _service.households(widget.userId);
      if (!mounted) return;
      setState(() {
        _data = data;
        _selected = {
          for (final e in data.associations.entries) e.key: [...e.value]
        };
        _preselect();
        _error = null;
      });
    } catch (_) {
      if (mounted)
        setState(() => _error = context.l10n.availabilityAccessReadFailed);
    }
  }

  void _preselect() {
    if (!_visitedProviders.add(_provider)) return;
    final servers = _data!.servers
        .where((s) => s.serviceType == _provider && s.eligible)
        .toList();
    if (servers.length == 1 &&
        !_data!.associations.containsKey(servers.single.instanceId)) {
      _selected.putIfAbsent(
          servers.single.instanceId, () => [...servers.single.libraryIds]);
    }
  }

  bool get _dirty {
    final saved = _data?.associations ?? const <String, List<String>>{};
    if (saved.length != _selected.length) return true;
    for (final e in saved.entries) {
      final selected = _selected[e.key];
      if (selected == null ||
          selected.length != e.value.length ||
          !selected.toSet().containsAll(e.value)) return true;
    }
    return false;
  }

  Future<void> _save() async {
    if (_selected.values.any((ids) => ids.isEmpty)) {
      setState(() => _error = context.l10n.availabilityChooseLibraries);
      return;
    }
    setState(() {
      _saving = true;
      _error = null;
    });
    try {
      await _service.saveHouseholds(widget.userId, _selected);
      if (mounted) Navigator.pop(context, true);
    } catch (_) {
      if (mounted)
        setState(() => _error = context.l10n.availabilityAccessSaveFailed);
    } finally {
      if (mounted) setState(() => _saving = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final l = context.l10n;
    final servers =
        _data?.servers.where((s) => s.serviceType == _provider).toList() ?? [];
    return UnsavedChangesGuard(
        isDialog: true,
        hasChanges: () => _dirty,
        child: AlertDialog(
            title: Text(l.availabilityLibraryAccessOnly),
            content: SizedBox(
                width: 520,
                child: SingleChildScrollView(
                    child: Column(
                        mainAxisSize: MainAxisSize.min,
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                      Text(l.availabilityHouseholdExplanation),
                      const SizedBox(height: 16),
                      DropdownButtonFormField<String>(
                          initialValue: _provider,
                          decoration: InputDecoration(
                              labelText: l.availabilityProvider),
                          items: const [
                            DropdownMenuItem(
                                value: 'plex', child: Text('Plex')),
                            DropdownMenuItem(
                                value: 'jellyfin', child: Text('Jellyfin')),
                            DropdownMenuItem(value: 'emby', child: Text('Emby'))
                          ],
                          onChanged: _saving
                              ? null
                              : (value) {
                                  if (value != null)
                                    setState(() {
                                      _provider = value;
                                      if (_data != null) _preselect();
                                    });
                                }),
                      if (_data == null && _error == null)
                        const Padding(
                            padding: EdgeInsets.all(24),
                            child: Center(child: CircularProgressIndicator())),
                      if (_data != null && servers.isEmpty)
                        Padding(
                            padding: const EdgeInsets.only(top: 12),
                            child: Text(l.availabilityNoEligibleServers)),
                      for (final server in servers) ...[
                        CheckboxListTile(
                            contentPadding: EdgeInsets.zero,
                            title: Text(server.name),
                            subtitle: server.linked
                                ? Text(l.availabilityLinkedAccountWins)
                                : server.error.isNotEmpty
                                    ? Text(l.availabilityServerUnconfirmed)
                                    : null,
                            value: _selected.containsKey(server.instanceId),
                            onChanged: _saving ||
                                    server.linked ||
                                    (!server.eligible &&
                                        !_selected
                                            .containsKey(server.instanceId))
                                ? null
                                : (checked) => setState(() {
                                      if (checked == true) {
                                        _selected[server.instanceId] = [
                                          ...server.libraryIds
                                        ];
                                      } else {
                                        _selected.remove(server.instanceId);
                                      }
                                    })),
                        if (_selected.containsKey(server.instanceId)) ...[
                          for (final id in {
                            ...server.libraryIds,
                            ..._selected[server.instanceId]!
                          })
                            Padding(
                                padding: const EdgeInsets.only(left: 16),
                                child: CheckboxListTile(
                                    contentPadding: EdgeInsets.zero,
                                    title: Text(server.libraries
                                            .where((l) => l.id == id)
                                            .firstOrNull
                                            ?.name ??
                                        l.availabilityUnknownLibrary(id)),
                                    subtitle: !server.libraryIds.contains(id)
                                        ? Text(l.availabilityLibraryNotScanned)
                                        : null,
                                    value: _selected[server.instanceId]!
                                        .contains(id),
                                    onChanged: _saving ||
                                            (!server.eligible &&
                                                !_selected[server.instanceId]!
                                                    .contains(id))
                                        ? null
                                        : (checked) => setState(() {
                                              final ids =
                                                  _selected[server.instanceId]!;
                                              if (checked == true) {
                                                ids.add(id);
                                              } else {
                                                ids.remove(id);
                                              }
                                            }))),
                        ],
                      ],
                      // Deleted or disabled servers stay explicit and removable.
                      for (final id in _selected.keys
                          .where((id) =>
                              !_data!.servers.any((s) => s.instanceId == id))
                          .toList())
                        CheckboxListTile(
                            contentPadding: EdgeInsets.zero,
                            title: Text(l.availabilityUnknownLibrary(id)),
                            subtitle: Text(l.availabilityLibraryNotScanned),
                            value: true,
                            onChanged: _saving
                                ? null
                                : (_) => setState(() => _selected.remove(id))),
                      if (_error != null) ...[
                        const SizedBox(height: 12),
                        Text(_error!),
                        if (_data == null)
                          TextButton(
                              onPressed: _load, child: Text(l.settingsRetry))
                      ],
                    ]))),
            actions: [
              TextButton(
                  onPressed: _saving ? null : () => Navigator.pop(context),
                  child: Text(l.settingsCancel)),
              FilledButton(
                  onPressed: _saving || _data == null || !_dirty ? null : _save,
                  child: Text(l.settingsSave))
            ]));
  }
}
