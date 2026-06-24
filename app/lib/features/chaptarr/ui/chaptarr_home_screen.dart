import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/providers/instance_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/error_banner.dart';
import '../data/chaptarr_api_service.dart';
import '../data/chaptarr_models.dart';
import '../logic/chaptarr_library_provider.dart';
import 'chaptarr_author_detail_screen.dart';
import 'chaptarr_author_list.dart';

/// Chaptarr library management screen (the Library tab of the Chaptarr module).
/// Instance-aware: uses the active Chaptarr instance from the instance provider.
class ChaptarrHomeScreen extends ConsumerStatefulWidget {
  const ChaptarrHomeScreen({super.key});

  @override
  ConsumerState<ChaptarrHomeScreen> createState() => _ChaptarrHomeScreenState();
}

class _ChaptarrHomeScreenState extends ConsumerState<ChaptarrHomeScreen> {
  ChaptarrLibraryNotifier? _notifier;
  final _searchController = TextEditingController();

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) => _initNotifier());
  }

  void _initNotifier() {
    final instanceState = ref.read(instanceProvider);
    final activeInstance = instanceState.activeChaptarrInstance;
    if (activeInstance == null) return;

    final backendDio = ref.read(backendClientProvider);
    final service = ChaptarrApiService(
      backendDio: backendDio,
      instanceId: activeInstance.id,
    );
    _notifier = ChaptarrLibraryNotifier(service);
    _notifier!.loadAuthors();
    setState(() {});
  }

  @override
  void dispose() {
    _searchController.dispose();
    super.dispose();
  }

  Future<void> _triggerAutomaticSearch(ChaptarrAuthor author) async {
    try {
      await _notifier!.searchForAuthor(author.id);
      if (!mounted) return;
      ScaffoldMessenger.of(context)
          .showSnackBar(const SnackBar(content: Text('Author search started')));
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text('Failed to start search: $e')));
    }
  }

  void _openAuthor(ChaptarrAuthor author) {
    final instanceId = ref.read(instanceProvider).activeChaptarrInstance?.id;
    if (instanceId == null) return;
    Navigator.of(context, rootNavigator: true).push(
      MaterialPageRoute(
        builder: (_) => ChaptarrAuthorDetailScreen(
          instanceId: instanceId,
          authorId: author.id,
          authorName: author.authorName,
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    // Rebuild when active instance changes
    ref.listen(instanceProvider.select((s) => s.activeChaptarrInstanceId),
        (_, __) => _initNotifier());

    if (_notifier == null) {
      return const Center(
          child: CircularProgressIndicator(color: AppTheme.accent));
    }

    return ListenableBuilder(
      listenable: _notifier!,
      builder: (context, _) {
        final state = _notifier!.state;

        return Column(
          children: [
            // Stats bar
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
              color: AppTheme.surface,
              child: Row(
                mainAxisAlignment: MainAxisAlignment.spaceAround,
                children: [
                  _StatChip(
                      label: 'Total',
                      count: state.authors.length,
                      color: AppTheme.textPrimary),
                  _StatChip(
                      label: 'Complete',
                      count: state.completeCount,
                      color: AppTheme.available),
                  _StatChip(
                      label: 'Partial',
                      count: state.partialCount,
                      color: AppTheme.requested),
                ],
              ),
            ),

            // Search + filter
            Padding(
              padding: const EdgeInsets.all(12),
              child: Row(
                children: [
                  Expanded(
                    child: TextField(
                      controller: _searchController,
                      onChanged: _notifier!.search,
                      decoration: InputDecoration(
                        hintText: 'Search authors...',
                        prefixIcon: const Icon(Icons.search),
                        suffixIcon: _searchController.text.isNotEmpty
                            ? IconButton(
                                icon: const Icon(Icons.close),
                                onPressed: () {
                                  _searchController.clear();
                                  _notifier!.search('');
                                },
                              )
                            : null,
                      ),
                    ),
                  ),
                  const SizedBox(width: 8),
                  PopupMenuButton<ChaptarrLibraryFilter>(
                    icon: const Icon(Icons.filter_list,
                        color: AppTheme.textPrimary),
                    onSelected: _notifier!.setFilter,
                    itemBuilder: (_) => ChaptarrLibraryFilter.values
                        .map((f) => PopupMenuItem(
                              value: f,
                              child: Row(
                                children: [
                                  if (f == state.filter)
                                    const Icon(Icons.check,
                                        size: 18, color: AppTheme.accent),
                                  if (f != state.filter)
                                    const SizedBox(width: 18),
                                  const SizedBox(width: 8),
                                  Text(f.name[0].toUpperCase() +
                                      f.name.substring(1)),
                                ],
                              ),
                            ))
                        .toList(),
                  ),
                ],
              ),
            ),

            if (state.error != null)
              ErrorBanner(
                message: state.error!,
                onRetry: _notifier!.loadAuthors,
              ),

            Expanded(
              child: state.isLoading && state.authors.isEmpty
                  ? const Center(
                      child: CircularProgressIndicator(color: AppTheme.accent))
                  : RefreshIndicator(
                      onRefresh: _notifier!.loadAuthors,
                      color: AppTheme.accent,
                      child: ChaptarrAuthorList(
                        authors: state.filtered,
                        onTap: _openAuthor,
                        onSearch: _triggerAutomaticSearch,
                        onDelete: (author) => _notifier!
                            .deleteAuthor(author.id, deleteFiles: false),
                      ),
                    ),
            ),
          ],
        );
      },
    );
  }
}

class _StatChip extends StatelessWidget {
  final String label;
  final int count;
  final Color color;

  const _StatChip({
    required this.label,
    required this.count,
    required this.color,
  });

  @override
  Widget build(BuildContext context) {
    return Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        Text(count.toString(),
            style: TextStyle(
                color: color, fontSize: 20, fontWeight: FontWeight.bold)),
        Text(label,
            style:
                const TextStyle(color: AppTheme.textSecondary, fontSize: 12)),
      ],
    );
  }
}
