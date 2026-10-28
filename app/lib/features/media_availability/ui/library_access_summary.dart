import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/network/backend_client.dart';
import '../../../l10n/l10n.dart';
import '../data/media_availability_service.dart';
import 'library_access_only_dialog.dart';

class LibraryAccessSummary extends ConsumerStatefulWidget {
  final int userId;
  const LibraryAccessSummary({super.key, required this.userId});
  @override
  ConsumerState<LibraryAccessSummary> createState() =>
      _LibraryAccessSummaryState();
}

class _LibraryAccessSummaryState extends ConsumerState<LibraryAccessSummary> {
  AvailabilityHouseholds? _data;
  bool _failed = false;
  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final data =
          await MediaAvailabilityService(ref.read(backendClientProvider))
              .households(widget.userId);
      if (mounted)
        setState(() {
          _data = data;
          _failed = false;
        });
    } catch (_) {
      if (mounted) setState(() => _failed = true);
    }
  }

  @override
  Widget build(BuildContext context) {
    final l = context.l10n;
    return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
      for (final entry in _data?.associations.entries ??
          const <MapEntry<String, List<String>>>[])
        Padding(
            padding: const EdgeInsets.only(bottom: 8),
            child: Text(
                '${l.availabilityLibraryAccessOnly}: ${_data!.servers.where((s) => s.instanceId == entry.key).firstOrNull?.name ?? l.availabilityUnknownLibrary(entry.key)}')),
      if (_failed) Text(l.availabilityAccessReadFailed),
      TextButton.icon(
          icon: const Icon(Icons.video_library_outlined),
          label: Text(l.availabilityLibraryAccessOnly),
          onPressed: () async {
            final saved = await showDialog<bool>(
                context: context,
                builder: (_) => LibraryAccessOnlyDialog(
                    userId: widget.userId,
                    initialProvider:
                        _data?.servers.firstOrNull?.serviceType ?? 'plex'));
            if (saved == true && mounted) await _load();
          }),
    ]);
  }
}
