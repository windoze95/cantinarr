import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:url_launcher/url_launcher.dart';

import '../../../core/widgets/app_sheet.dart';
import '../data/listen_links.dart';
import '../logic/listen_links_provider.dart';

final bookListenLauncherProvider = Provider<Future<bool> Function(Uri)>(
    (ref) => (uri) => launchUrl(uri, mode: LaunchMode.externalApplication));

/// One action per granted server, shown only beside an available audiobook.
/// Generic shortcuts never imply a verified title. Multiple exact copies stay
/// separate and the person chooses the library/narration they want.
class BookListenActions extends ConsumerStatefulWidget {
  final String instanceId, foreignBookId;
  final int refreshTick;
  const BookListenActions(
      {super.key,
      required this.instanceId,
      required this.foreignBookId,
      this.refreshTick = 0});

  @override
  ConsumerState<BookListenActions> createState() => _BookListenActionsState();
}

class _BookListenActionsState extends ConsumerState<BookListenActions>
    with WidgetsBindingObserver {
  ListenRequest get _request => (
        instanceId: widget.instanceId,
        foreignId: widget.foreignBookId,
        refreshTick: widget.refreshTick
      );

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) {
      ref.invalidate(listenLinksProvider(_request));
    }
  }

  void _retry() => ref.invalidate(listenLinksProvider(_request));

  Future<void> _open(String address) async {
    final uri = Uri.tryParse(address);
    var opened = false;
    if (uri != null &&
        {'http', 'https'}.contains(uri.scheme) &&
        uri.host.isNotEmpty &&
        uri.userInfo.isEmpty) {
      try {
        opened = await ref.read(bookListenLauncherProvider)(uri);
      } catch (_) {/* The browser may refuse a launch. */}
    }
    if (!opened && mounted) {
      ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text("Couldn't open Audiobookshelf.")));
    }
  }

  Future<void> _choose(ListenLink link) async {
    if (link.items.length == 1) {
      await _open(link.items.single.url);
      return;
    }
    final item = await showAppSheet<ListenItem>(
      context,
      builder: (context) => SafeArea(
          child: Padding(
              padding: const EdgeInsets.all(20),
              child: Column(
                mainAxisSize: MainAxisSize.min,
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text('Choose an audiobook',
                      style: Theme.of(context).textTheme.titleLarge),
                  const SizedBox(height: 12),
                  Flexible(
                      child: ListView(shrinkWrap: true, children: [
                    for (final item in link.items)
                      ListTile(
                        key: ValueKey(
                            'listen-item:${link.instanceId}:${item.id}'),
                        leading: const Icon(Icons.headphones),
                        title: Text(item.title),
                        subtitle: Text([
                          link.name,
                          item.libraryName,
                          ...item.narrators
                        ].where((text) => text.isNotEmpty).join(' · ')),
                        onTap: () => Navigator.of(context).pop(item),
                      ),
                  ])),
                ],
              ))),
    );
    if (item != null && mounted) await _open(item.url);
  }

  @override
  Widget build(BuildContext context) =>
      ref.watch(listenLinksProvider(_request)).when(
            skipLoadingOnRefresh: false,
            skipLoadingOnReload: false,
            loading: () => const SizedBox.shrink(),
            error: (error, stack) => TextButton.icon(
                onPressed: _retry,
                icon: const Icon(Icons.refresh),
                label: const Text("Couldn't check Audiobookshelf · Retry")),
            data: (links) =>
                Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
              for (final link in links)
                Padding(
                    padding: const EdgeInsets.symmetric(vertical: 4),
                    child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          if (link.state == 'found' && link.items.isNotEmpty)
                            OutlinedButton.icon(
                                onPressed: () => _choose(link),
                                icon: const Icon(Icons.headphones),
                                label: Text(links.length == 1
                                    ? 'Listen in Audiobookshelf'
                                    : 'Listen in Audiobookshelf · ${link.name}'))
                          else if (link.fallbackUrl.isNotEmpty)
                            OutlinedButton.icon(
                                onPressed: () => _open(link.fallbackUrl),
                                icon: const Icon(Icons.open_in_new),
                                label: Text(links.length == 1
                                    ? 'Open Audiobookshelf'
                                    : 'Open Audiobookshelf · ${link.name}')),
                        ])),
            ]),
          );
}
