import 'package:cantinarr/l10n/l10n.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:url_launcher/url_launcher.dart';

const cantinarrSetupGuideUrl = 'https://docs.cantinarr.com/start/quickstart/';

final serverSetupGuideLauncherProvider = Provider<Future<bool> Function(Uri)>(
    (ref) => (uri) => launchUrl(uri, mode: LaunchMode.externalApplication));

class ServerSetupHelp extends ConsumerWidget {
  const ServerSetupHelp({super.key});

  Future<void> _open(BuildContext context, WidgetRef ref) async {
    var opened = false;
    try {
      opened = await ref.read(serverSetupGuideLauncherProvider)(
          Uri.parse(cantinarrSetupGuideUrl));
    } catch (_) {
      // Offer the same recovery when the platform throws or refuses the URL.
    }
    if (opened || !context.mounted) return;
    await showDialog<void>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text(context.l10n.authOpenSetupGuide),
        content: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(context.l10n.authBrowserSetupHelp),
            SizedBox(height: 16),
            SelectableText(cantinarrSetupGuideUrl),
          ],
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(context).pop(),
            child: Text(context.l10n.authClose),
          ),
          TextButton(
            onPressed: () async {
              await Clipboard.setData(
                  const ClipboardData(text: cantinarrSetupGuideUrl));
              if (!context.mounted) return;
              final messenger = ScaffoldMessenger.of(context);
              Navigator.of(context).pop();
              messenger.showSnackBar(SnackBar(
                content: Text(context.l10n.authSetupAddressCopied),
              ));
            },
            child: Text(context.l10n.authCopyAddress),
          ),
        ],
      ),
    );
  }

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        Text(context.l10n.authOwnServerQuestion,
            style: Theme.of(context).textTheme.bodyMedium),
        TextButton.icon(
          onPressed: () => _open(context, ref),
          icon: const Icon(Icons.open_in_new, size: 16),
          label:
              Text(context.l10n.authSetupServer, textAlign: TextAlign.center),
        ),
      ],
    );
  }
}
