import 'package:flutter/material.dart';
import '../../../l10n/l10n.dart';
import '../../discover/ui/browse_labels.dart';
import '../../request/data/request_service.dart';
import '../../request/logic/request_provider.dart';

/// Keeps playback coverage, acquisition and future-episode following visible
/// together without treating a household association as playback credentials.
class MediaAvailabilityPanel extends StatefulWidget {
  final RequestNotifier notifier;
  final VoidCallback? onLibraryCopy;
  final VoidCallback? onFollow;
  const MediaAvailabilityPanel(
      {super.key, required this.notifier, this.onLibraryCopy, this.onFollow});
  @override
  State<MediaAvailabilityPanel> createState() => _MediaAvailabilityPanelState();
}

class _MediaAvailabilityPanelState extends State<MediaAvailabilityPanel> {
  int? _stopping;
  String? _error;
  Future<void> _stop(MediaFollowing follow) async {
    setState(() {
      _stopping = follow.requestId;
      _error = null;
    });
    try {
      await widget.notifier.stopFollowing(follow.requestId);
    } catch (_) {
      if (mounted) setState(() => _error = context.l10n.availabilityStopFailed);
    } finally {
      if (mounted) setState(() => _stopping = null);
    }
  }

  @override
  Widget build(BuildContext context) {
    final l = context.l10n;
    final state = widget.notifier.state;
    final availability = state.availability;
    if (availability == null && state.following.isEmpty)
      return const SizedBox.shrink();
    final names = availability?.sources
            .where((source) =>
                source.state == 'available' ||
                source.state == 'partial' ||
                source.state == 'found')
            .map((source) => source.name)
            .where((name) => name.isNotEmpty)
            .toSet()
            .join(', ') ??
        '';
    return Padding(
        padding: const EdgeInsets.only(top: 10),
        child: Column(children: [
          if (availability?.hasFiles == true && names.isNotEmpty)
            Text(l.availabilityOnServers(names), textAlign: TextAlign.center),
          if (availability?.isKnown == false) ...[
            Text(l.availabilityStatusUnknown, textAlign: TextAlign.center),
            TextButton.icon(
                onPressed:
                    state.isCheckingStatus ? null : widget.notifier.checkStatus,
                icon: const Icon(Icons.refresh),
                label: Text(l.availabilityRefresh)),
          ],
          if (availability?.hasFiles == true &&
              state.status != state.displayStatus &&
              (state.status == RequestStatus.pending ||
                  state.status == RequestStatus.requested ||
                  state.status == RequestStatus.downloading))
            Text(
                l.availabilityLibraryRequest(
                    browseLabel(context, state.status.label)),
                textAlign: TextAlign.center),
          if ((availability?.hasFiles == true || availability?.isKnown == false) && widget.onLibraryCopy != null)
            TextButton.icon(
                onPressed: state.isRequesting ? null : widget.onLibraryCopy,
                icon: const Icon(Icons.library_add_outlined),
                label: Text(l.availabilityRequestLibraryCopy)),
          if (widget.onFollow != null)
            TextButton.icon(
                onPressed: state.isRequesting ? null : widget.onFollow,
                icon: const Icon(Icons.playlist_add),
                label: Text(l.availabilityFollowSeasons)),
          for (final follow in state.following)
            Padding(
                padding: const EdgeInsets.only(top: 8),
                child: Column(children: [
                  Text(
                      switch (follow.state) {
                        'active' => l.availabilityFollowing,
                        'paused' => l.availabilityFollowingPaused,
                        'stopped' => l.availabilityFollowingStopped,
                        _ => l.availabilityFollowingAttention
                      },
                      textAlign: TextAlign.center),
                  if (follow.seasons.isNotEmpty)
                    Text(
                        l.availabilityFollowingSeasons(
                            follow.seasons.join(', ')),
                        textAlign: TextAlign.center),
                  if (follow.message.isNotEmpty)
                    Text(follow.message, textAlign: TextAlign.center),
                  if (follow.canStop)
                    TextButton(
                        onPressed:
                            _stopping != null ? null : () => _stop(follow),
                        child: Text(l.availabilityStopFollowing)),
                ])),
          if (_error != null) Text(_error!),
        ]));
  }
}
