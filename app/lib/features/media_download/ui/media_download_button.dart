import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:url_launcher/url_launcher.dart';

import '../../../core/widgets/app_sheet.dart';
import '../data/media_download_models.dart';
import '../data/media_download_service.dart';
import '../logic/media_coverage_provider.dart';

typedef MediaDownloadLauncher = Future<bool> Function(Uri uri);

final mediaDownloadLauncherProvider = Provider<MediaDownloadLauncher>(
  (_) => (uri) => kIsWeb
      ? launchUrl(uri, webOnlyWindowName: '_self')
      : launchUrl(uri, mode: LaunchMode.externalApplication),
);

class MediaDownloadButton extends ConsumerStatefulWidget {
  final String instanceId;
  final int fileId;
  final String label;
  final bool iconOnly;
  final bool outlined;

  /// The file's arr-reported path. When set, the button removes itself once
  /// the server confirms no mapping covers the path; unknown verdicts fail
  /// open (ticket issuance stays the authority).
  final String? reportedPath;

  const MediaDownloadButton({
    super.key,
    required this.instanceId,
    required this.fileId,
    required this.label,
    this.iconOnly = false,
    this.outlined = false,
    this.reportedPath,
  });

  @override
  ConsumerState<MediaDownloadButton> createState() =>
      _MediaDownloadButtonState();
}

class _MediaDownloadButtonState extends ConsumerState<MediaDownloadButton> {
  bool _busy = false;

  Future<void> _download() async {
    if (_busy || widget.fileId <= 0) return;
    setState(() => _busy = true);
    try {
      final service = ref.read(mediaDownloadServiceProvider);
      final ticket = await service.createTicket(
        instanceId: widget.instanceId,
        fileId: widget.fileId,
      );
      final launcher = ref.read(mediaDownloadLauncherProvider);
      final opened = await launcher(ticket.url);
      if (!opened) {
        throw const MediaDownloadException(
          'Could not open the download. Try again.',
        );
      }
    } on MediaDownloadException catch (error) {
      if (mounted) {
        ScaffoldMessenger.of(context)
            .showSnackBar(SnackBar(content: Text(error.message)));
      }
    } catch (_) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(const SnackBar(
          content: Text('Could not prepare the download. Try again.'),
        ));
      }
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Widget get _icon => _busy
      ? const SizedBox(
          width: 18,
          height: 18,
          child: CircularProgressIndicator(strokeWidth: 2),
        )
      : const Icon(Icons.download_rounded, size: 19);

  @override
  Widget build(BuildContext context) {
    final path = widget.reportedPath?.trim() ?? '';
    if (path.isNotEmpty) {
      final key = MediaCoverageNotifier.keyFor(widget.instanceId, path);
      final verdict = ref
          .watch(mediaCoverageProvider.select((verdicts) => verdicts[key]));
      ref.read(mediaCoverageProvider.notifier).ensure(widget.instanceId, path);
      if (verdict == false) return const SizedBox.shrink();
    }
    final onPressed = _busy || widget.fileId <= 0 ? null : _download;
    if (widget.iconOnly) {
      return IconButton(
        tooltip: widget.label,
        onPressed: onPressed,
        icon: _icon,
      );
    }
    if (widget.outlined) {
      return OutlinedButton.icon(
        onPressed: onPressed,
        icon: _icon,
        label: Text(widget.label),
      );
    }
    return TextButton.icon(
      onPressed: onPressed,
      icon: _icon,
      label: Text(widget.label),
    );
  }
}

class MediaDownloadChoiceButton extends ConsumerWidget {
  final String instanceId;
  final List<MediaDownloadChoice> choices;
  final String label;
  final String sheetTitle;
  final bool iconOnly;
  final bool outlined;

  const MediaDownloadChoiceButton({
    super.key,
    required this.instanceId,
    required this.choices,
    required this.label,
    required this.sheetTitle,
    this.iconOnly = false,
    this.outlined = false,
  });

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final verdicts = ref.watch(mediaCoverageProvider);
    final notifier = ref.read(mediaCoverageProvider.notifier);
    var anyCandidate = false;
    final available = <MediaDownloadChoice>[];
    for (final choice in choices) {
      if (choice.fileId <= 0) continue;
      anyCandidate = true;
      final path = choice.reportedPath?.trim() ?? '';
      if (path.isNotEmpty) {
        notifier.ensure(instanceId, path);
        if (verdicts[MediaCoverageNotifier.keyFor(instanceId, path)] ==
            false) {
          continue;
        }
      }
      available.add(choice);
    }
    // Every real candidate is confirmed outside the instance's mappings:
    // offering a picker of guaranteed failures helps nobody.
    if (available.isEmpty && anyCandidate) {
      return const SizedBox.shrink();
    }
    if (available.length == 1) {
      return MediaDownloadButton(
        instanceId: instanceId,
        fileId: available.single.fileId,
        label: label,
        iconOnly: iconOnly,
        outlined: outlined,
        reportedPath: available.single.reportedPath,
      );
    }

    final onPressed =
        available.isEmpty ? null : () => _showChoices(context, available);
    if (iconOnly) {
      return IconButton(
        tooltip: label,
        onPressed: onPressed,
        icon: const Icon(Icons.download_rounded, size: 19),
      );
    }
    if (outlined) {
      return OutlinedButton.icon(
        onPressed: onPressed,
        icon: const Icon(Icons.download_rounded, size: 19),
        label: Text(label),
      );
    }
    return TextButton.icon(
      onPressed: onPressed,
      icon: const Icon(Icons.download_rounded, size: 19),
      label: Text(label),
    );
  }

  Future<void> _showChoices(
    BuildContext context,
    List<MediaDownloadChoice> available,
  ) {
    return showAppSheet<void>(
      context,
      builder: (_) => AppSheet(
        padding: const EdgeInsets.fromLTRB(16, 0, 16, 16),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(sheetTitle, style: Theme.of(context).textTheme.titleLarge),
            const SizedBox(height: 10),
            for (final choice in available)
              ListTile(
                contentPadding: const EdgeInsets.symmetric(horizontal: 4),
                title: Text(choice.label),
                subtitle:
                    choice.subtitle == null ? null : Text(choice.subtitle!),
                trailing: MediaDownloadButton(
                  instanceId: instanceId,
                  fileId: choice.fileId,
                  label: 'Download ${choice.label}',
                  iconOnly: true,
                  reportedPath: choice.reportedPath,
                ),
              ),
          ],
        ),
      ),
    );
  }
}
