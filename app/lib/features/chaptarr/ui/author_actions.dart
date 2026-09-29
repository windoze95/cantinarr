import 'package:flutter/material.dart';
import '../../../core/network/library_settings_service.dart';
import '../../../core/widgets/action_sheet.dart';
import '../../../core/widgets/library_actions.dart';
import '../../../core/widgets/library_settings_screen.dart';
import '../../../navigation/ambient_page_route.dart';
import '../data/chaptarr_api_service.dart';
import '../data/chaptarr_models.dart';
import 'chaptarr_releases_screen.dart';

Future<void> showAuthorActions(BuildContext context, {
  required ChaptarrApiService service,
  required LibrarySettingsService settings,
  required String instanceId,
  required ChaptarrAuthor author,
  LibraryAction? selectedAction,
  VoidCallback? onChanged,
  VoidCallback? onRemoved,
}) async {
  final action = selectedAction ?? await showActionSheet<LibraryAction>(
    context, title: author.authorName,
    actions: libraryActions('author'),
  );
  if (action == null || !context.mounted) return;
  void toast(String message) {
    if (context.mounted) {
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(message)));
    }
  }
  try {
    switch (action) {
      case LibraryAction.search:
        await service.searchAuthor(author.id);
        toast('Search queued for ${author.authorName}');
      case LibraryAction.interactive:
        final records = (await service.getBooks(authorId: author.id))
            .where((record) => record.authorId == author.id).toList()
          ..sort((a, b) => a.title.toLowerCase().compareTo(b.title.toLowerCase()));
        if (!context.mounted) return;
        if (records.isEmpty) { toast('No books in this author’s library'); return; }
        final id = await showActionSheet<int>(context,
          title: 'Select book and format', actions: [
            for (final record in records) SheetAction(record.id, Icons.menu_book, "${record.title} · ${record.format == BookFormat.audiobook ? 'Audiobook' : 'eBook'}"),
          ]);
        if (id == null || !context.mounted) return;
        final selected = records.firstWhere((r) => r.id == id);
        await Navigator.of(context, rootNavigator: true).push(AmbientPageRoute(
          builder: (_) => ChaptarrReleasesScreen(instanceId: instanceId,
            bookId: id, bookTitle: selected.title)));
      case LibraryAction.edit:
      case LibraryAction.monitoring:
        final saved = await Navigator.of(context, rootNavigator: true).push<bool>(AmbientPageRoute(
          builder: (_) => LibrarySettingsScreen(service: settings, title: author.authorName,
            monitoringOnly: action == LibraryAction.monitoring)));
        if (saved == true && context.mounted) { toast('Settings saved'); onChanged?.call(); }
      case LibraryAction.refresh:
        await service.refreshAuthor(author.id);
        toast('Refresh queued for ${author.authorName}');
        if (context.mounted) onChanged?.call();
      case LibraryAction.rescan:
        await service.rescanAuthor(author.id);
        toast('Rescan queued for ${author.authorName}');
        if (context.mounted) onChanged?.call();
      case LibraryAction.remove:
        final deleteFiles = await confirmLibraryRemoval(context, title: author.authorName,
          noun: 'author', service: 'Chaptarr');
        if (deleteFiles == null) return;
        await service.deleteAuthor(author.id, deleteFiles: deleteFiles);
        toast('Removed ${author.authorName}');
        if (context.mounted) onRemoved?.call();
    }
  } catch (e) { toast('Action failed: $e'); }
}
