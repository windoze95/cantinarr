import 'package:flutter/material.dart';
import 'package:url_launcher/url_launcher.dart';

import '../../../../core/theme/app_theme.dart';
import '../../../media_detail/logic/title_links.dart';

/// How a chip opens its page. The default hands the address to the platform;
/// tests swap it, since no launcher answers there.
typedef BookLinkLauncher = Future<bool> Function(Uri uri);

Future<bool> _launchExternally(Uri uri) =>
    launchUrl(uri, mode: LaunchMode.externalApplication);

/// A book's outbound links (Goodreads, Open Library, Hardcover) as the same
/// chips the title page's Links line uses: marked outbound, opening the
/// book's own page in the browser, or the site's app when it claims the
/// address, and saying so when nothing could open it. Shared by the requester
/// book page and the Chaptarr book sheet.
class BookLinkChips extends StatelessWidget {
  final List<TitleLink> links;
  final BookLinkLauncher launch;

  const BookLinkChips(
    this.links, {
    super.key,
    this.launch = _launchExternally,
  });

  @override
  Widget build(BuildContext context) {
    return Wrap(
      spacing: 6,
      runSpacing: 6,
      children: [
        for (final link in links)
          ActionChip(
            avatar: const Icon(Icons.open_in_new,
                size: 14, color: AppTheme.textSecondary),
            label: Text(link.label, style: const TextStyle(fontSize: 12)),
            tooltip: 'Open on ${link.label}',
            backgroundColor: AppTheme.surfaceVariant,
            side: const BorderSide(color: AppTheme.border),
            materialTapTargetSize: MaterialTapTargetSize.shrinkWrap,
            visualDensity: VisualDensity.compact,
            onPressed: () => _open(context, link),
          ),
      ],
    );
  }

  Future<void> _open(BuildContext context, TitleLink link) async {
    // Resolved before the await: the chip may be gone by the time the launch
    // reports back, but the messenger it would speak through is not.
    final messenger = ScaffoldMessenger.maybeOf(context);
    var opened = false;
    try {
      opened = await launch(Uri.parse(link.url));
    } catch (_) {
      opened = false;
    }
    if (opened) return;
    messenger?.showSnackBar(SnackBar(
      content: Text("Couldn't open ${link.label}."),
    ));
  }
}
