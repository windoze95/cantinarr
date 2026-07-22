import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:url_launcher/url_launcher.dart';

import '../../../core/theme/app_theme.dart';
import '../data/media_download_models.dart';
import '../data/media_download_service.dart';

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

  const MediaDownloadButton({
    super.key,
    required this.instanceId,
    required this.fileId,
    required this.label,
    this.iconOnly = false,
    this.outlined = false,
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

class MediaDownloadChoiceButton extends StatelessWidget {
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
  Widget build(BuildContext context) {
    final available =
        choices.where((choice) => choice.fileId > 0).toList(growable: false);
    if (available.length == 1) {
      return MediaDownloadButton(
        instanceId: instanceId,
        fileId: available.single.fileId,
        label: label,
        iconOnly: iconOnly,
        outlined: outlined,
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
    return showModalBottomSheet<void>(
      context: context,
      backgroundColor: Colors.transparent,
      isScrollControlled: true,
      builder: (_) => Material(
        color: AppTheme.surface,
        borderRadius: const BorderRadius.vertical(top: Radius.circular(20)),
        clipBehavior: Clip.antiAlias,
        child: SafeArea(
          top: false,
          child: ConstrainedBox(
            constraints: BoxConstraints(
              maxHeight: MediaQuery.sizeOf(context).height * 0.75,
            ),
            child: ListView(
              shrinkWrap: true,
              padding: const EdgeInsets.fromLTRB(16, 18, 16, 16),
              children: [
                Text(sheetTitle,
                    style: Theme.of(context).textTheme.titleLarge),
                const SizedBox(height: 10),
                for (final choice in available)
                  ListTile(
                    contentPadding: const EdgeInsets.symmetric(horizontal: 4),
                    title: Text(choice.label),
                    subtitle: choice.subtitle == null
                        ? null
                        : Text(choice.subtitle!),
                    trailing: MediaDownloadButton(
                      instanceId: instanceId,
                      fileId: choice.fileId,
                      label: 'Download ${choice.label}',
                      iconOnly: true,
                    ),
                  ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}
