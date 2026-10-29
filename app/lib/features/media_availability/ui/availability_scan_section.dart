import 'dart:async';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:intl/intl.dart';
import '../../../core/network/backend_client.dart';
import '../../../l10n/l10n.dart';
import '../../settings/data/instance_api_service.dart';
import '../data/media_availability_service.dart';

/// Edits only scan configuration. Account-sharing choices are independent.
class AvailabilityScanSection extends ConsumerStatefulWidget {
  final String? instanceId;
  final AvailabilityScanConfig value;
  final bool unsaved;
  final ValueChanged<AvailabilityScanConfig> onChanged;
  final List<MediaServerLibrary>? initialLibraries;
  const AvailabilityScanSection(
      {super.key,
      this.instanceId,
      required this.value,
      required this.onChanged,
      this.unsaved = false,
      this.initialLibraries});
  @override
  ConsumerState<AvailabilityScanSection> createState() =>
      _AvailabilityScanSectionState();
}

class _AvailabilityScanSectionState
    extends ConsumerState<AvailabilityScanSection> {
  List<MediaServerLibrary>? _libraries;
  MediaScanStatus? _status;
  bool _loading = false, _running = false;
  String? _error;
  Timer? _poll;
  MediaAvailabilityService get _service =>
      MediaAvailabilityService(ref.read(backendClientProvider));
  @override
  void initState() {
    super.initState();
    if (widget.instanceId != null) _load();
  }

  @override
  void dispose() {
    _poll?.cancel();
    super.dispose();
  }

  Future<void> _load({bool scan = false}) async {
    final id = widget.instanceId;
    if (id == null) return;
    setState(() {
      _loading = true;
      _running = scan;
      _error = null;
    });
    try {
      final status = scan ? await _service.scan(id) : await _service.status(id);
      final libraries = await _service.libraries(id);
      if (!mounted) return;
      setState(() {
        _status = status;
        _libraries = libraries;
      });
      _poll?.cancel();
      if (status.running) _poll = Timer(const Duration(seconds: 3), _load);
    } catch (_) {
      if (mounted) setState(() => _error = context.l10n.availabilityScanFailed);
    } finally {
      if (mounted)
        setState(() {
          _loading = false;
          _running = false;
        });
    }
  }

  @override
  Widget build(BuildContext context) {
    final l = context.l10n;
    final libraries = (_libraries ?? widget.initialLibraries)
        ?.where((library) => const {
              '',
              'mixed',
              'movie',
              'movies',
              'show',
              'tv',
              'tvshows'
            }.contains(library.collectionType.toLowerCase()))
        .toList();
    final selected = widget.value.libraryIds.toSet();
    final unknown = selected
        .where((id) => !(libraries?.any((lib) => lib.id == id) ?? false));
    return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
      SwitchListTile(
          contentPadding: EdgeInsets.zero,
          title: Text(l.availabilityScanTitle),
          subtitle: Text(l.availabilityScanExplanation),
          value: widget.value.enabled,
          onChanged: (enabled) => widget.onChanged(AvailabilityScanConfig(
              enabled: enabled, libraryIds: widget.value.libraryIds))),
      if (widget.value.enabled) ...[
        Text(l.availabilityScanLibraries),
        if (libraries == null && _loading) const LinearProgressIndicator(),
        for (final library in libraries ?? const <MediaServerLibrary>[])
          CheckboxListTile(
              contentPadding: EdgeInsets.zero,
              title: Text(library.name),
              value: selected.contains(library.id),
              onChanged: (checked) {
                if (checked == true) {
                  selected.add(library.id);
                } else {
                  selected.remove(library.id);
                }
                widget.onChanged(AvailabilityScanConfig(
                    enabled: true, libraryIds: selected.toList()));
              }),
        for (final id in unknown)
          CheckboxListTile(
              contentPadding: EdgeInsets.zero,
              title: Text(l.availabilityUnknownLibrary(id)),
              value: true,
              onChanged: (_) {
                selected.remove(id);
                widget.onChanged(AvailabilityScanConfig(
                    enabled: true, libraryIds: selected.toList()));
              }),
        if (selected.isEmpty) Text(l.availabilityChooseLibraries),
      ],
      if (_status != null) ...[
        if (_status!.running) Text(l.availabilityScanning),
        if (_status!.lastSuccessAt != null)
          Text(l.availabilityLastScan(
              DateFormat.yMd()
                  .add_jm()
                  .format(_status!.lastSuccessAt!.toLocal()),
              _status!.itemCount)),
        if (_status!.lastSuccessAt == null && !_status!.running)
          Text(l.availabilityNotScanned),
        if (_status!.error.isNotEmpty) Text(l.availabilityScanFailed),
      ],
      if (_error != null) Text(_error!),
      if (widget.unsaved || widget.instanceId == null)
        Text(l.availabilitySaveBeforeScan),
      if (widget.instanceId != null)
        Wrap(spacing: 8, children: [
          OutlinedButton.icon(
              onPressed: _loading ||
                      widget.unsaved ||
                      !widget.value.enabled ||
                      selected.isEmpty ||
                      _status?.running == true
                  ? null
                  : () => _load(scan: true),
              icon: const Icon(Icons.sync),
              label: Text(
                  _running ? l.availabilityScanning : l.availabilityScanNow)),
          TextButton.icon(
              onPressed: _loading ? null : _load,
              icon: const Icon(Icons.refresh),
              label: Text(l.availabilityRefresh)),
        ]),
    ]);
  }
}
