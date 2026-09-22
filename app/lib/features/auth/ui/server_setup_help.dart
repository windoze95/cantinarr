import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:url_launcher/url_launcher.dart';

const cantinarrSetupGuideUrl = 'https://docs.cantinarr.com/start/quickstart/';

final serverSetupGuideLauncherProvider =
    Provider<Future<bool> Function(Uri)>((ref) =>
        (uri) => launchUrl(uri, mode: LaunchMode.externalApplication));

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
        title: const Text('Open the setup guide'),
        content: const Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text('Could not open your browser. Copy this address and open it '
                'in a browser to install your Cantinarr server.'),
            SizedBox(height: 16),
            SelectableText(cantinarrSetupGuideUrl),
          ],
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(context).pop(),
            child: const Text('Close'),
          ),
          TextButton(
            onPressed: () async {
              await Clipboard.setData(
                  const ClipboardData(text: cantinarrSetupGuideUrl));
              if (!context.mounted) return;
              final messenger = ScaffoldMessenger.of(context);
              Navigator.of(context).pop();
              messenger.showSnackBar(const SnackBar(
                content: Text('Setup guide address copied'),
              ));
            },
            child: const Text('Copy address'),
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
        Text('Setting up your own server?',
            style: Theme.of(context).textTheme.bodyMedium),
        TextButton.icon(
          onPressed: () => _open(context, ref),
          icon: const Icon(Icons.open_in_new, size: 16),
          label: const Text('Set up a Cantinarr server',
              textAlign: TextAlign.center),
        ),
      ],
    );
  }
}
