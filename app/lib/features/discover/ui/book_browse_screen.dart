import 'package:cantinarr/l10n/l10n.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import '../../../core/providers/instance_provider.dart';
import '../logic/book_discovery_provider.dart';
import 'catalog_setup_button.dart';

class BookBrowseScreen extends StatelessWidget {
  final BookBrowseQuery query;
  const BookBrowseScreen({super.key, required this.query});
  @override
  Widget build(BuildContext context) =>
      BookCatalogRetiredScreen(instanceId: query.instanceId);
}

class NativeBookSearchButton extends ConsumerWidget {
  final String title;
  final String? instanceId;
  const NativeBookSearchButton({super.key, this.title = '', this.instanceId});
  @override
  Widget build(BuildContext context, WidgetRef ref) => TextButton.icon(
        icon: const Icon(Icons.search),
        label: Text(context.l10n.browseSearchBooks),
        onPressed: () {
          ref.read(bookDiscoverySearchSeedProvider.notifier).state =
              (query: title, instanceId: instanceId);
          context.go('/dashboard/books');
        },
      );
}

class BookCatalogRetiredScreen extends ConsumerWidget {
  final String title;
  final String? instanceId;
  const BookCatalogRetiredScreen({super.key, this.title = '', this.instanceId});
  @override
  Widget build(BuildContext context, WidgetRef ref) => Scaffold(
        appBar: AppBar(title: Text(context.l10n.browseBookDetails)),
        body: Center(
            child: Padding(
                padding: const EdgeInsets.all(24),
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    if (title.isNotEmpty)
                      Text(title,
                          style: Theme.of(context).textTheme.titleLarge),
                    const SizedBox(height: 16),
                    Text(
                        context.l10n
                            .browseOpenLibraryBookDiscoveryHasRetiredSearchYourChaptarrLibraryAndSelectTheBookToRequest,
                        textAlign: TextAlign.center),
                    const SizedBox(height: 16),
                    NativeBookSearchButton(
                        title: title, instanceId: instanceId),
                    if (ref.watch(instanceProvider).activeChaptarrInstance ==
                        null)
                      const CatalogSetupButton(serviceType: 'chaptarr'),
                  ],
                ))),
      );
}
