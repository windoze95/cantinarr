import 'package:cantinarr/l10n/l10n.dart';
import 'package:flutter/material.dart';
import '../../../core/network/library_settings_service.dart';
import '../../../core/widgets/action_sheet.dart';
import '../../../core/widgets/library_actions.dart';
import '../../../core/widgets/library_settings_screen.dart';
import '../../../navigation/ambient_page_route.dart';
import '../data/chaptarr_api_service.dart';
import '../data/chaptarr_models.dart';
import 'chaptarr_releases_screen.dart';

Future<void> showAuthorActions(
  BuildContext context, {
  required ChaptarrApiService service,
  required LibrarySettingsService settings,
  required String instanceId,
  required ChaptarrAuthor author,
  LibraryAction? selectedAction,
  VoidCallback? onChanged,
  VoidCallback? onRemoved,
}) async {
  final action = selectedAction ??
      await showActionSheet<LibraryAction>(
        context,
        title: author.authorName,
        actions: libraryActions('author'),
      );
  if (action == null || !context.mounted) return;
  void toast(String message) {
    if (context.mounted) {
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text(message)));
    }
  }

  try {
    switch (action) {
      case LibraryAction.search:
        await service.searchAuthor(author.id);
        toast(
            context.l10n.adminSearchQueuedFor((author.authorName).toString()));
      case LibraryAction.interactive:
        final records = (await service.getBooks(authorId: author.id))
            .where((record) => record.authorId == author.id)
            .toList()
          ..sort(
              (a, b) => a.title.toLowerCase().compareTo(b.title.toLowerCase()));
        if (!context.mounted) return;
        if (records.isEmpty) {
          toast(context.l10n.adminNoBooksInThisAuthorSLibrary);
          return;
        }
        final id = await showActionSheet<int>(context,
            title: context.l10n.adminSelectBookAndFormat,
            actions: [
              for (final record in records)
                SheetAction(record.id, Icons.menu_book,
                    "${record.title} · ${record.format == BookFormat.audiobook ? 'Audiobook' : 'eBook'}"),
            ]);
        if (id == null || !context.mounted) return;
        final selected = records.firstWhere((r) => r.id == id);
        await Navigator.of(context, rootNavigator: true).push(AmbientPageRoute(
            builder: (_) => ChaptarrReleasesScreen(
                instanceId: instanceId,
                bookId: id,
                bookTitle: selected.title)));
      case LibraryAction.edit:
      case LibraryAction.monitoring:
        final saved = await Navigator.of(context, rootNavigator: true)
            .push<bool>(AmbientPageRoute(
                builder: (_) => LibrarySettingsScreen(
                    service: settings,
                    title: author.authorName,
                    monitoringOnly: action == LibraryAction.monitoring)));
        if (saved == true && context.mounted) {
          toast(context.l10n.adminSettingsSaved);
          onChanged?.call();
        }
      case LibraryAction.refresh:
        await service.refreshAuthor(author.id);
        toast(
            context.l10n.adminRefreshQueuedFor((author.authorName).toString()));
        if (context.mounted) onChanged?.call();
      case LibraryAction.rescan:
        await service.rescanAuthor(author.id);
        toast(
            context.l10n.adminRescanQueuedFor((author.authorName).toString()));
        if (context.mounted) onChanged?.call();
      case LibraryAction.remove:
        final deleteFiles = await confirmLibraryRemoval(context,
            title: author.authorName, noun: 'author', service: 'Chaptarr');
        if (deleteFiles == null) return;
        await service.deleteAuthor(author.id, deleteFiles: deleteFiles);
        toast(context.l10n.adminRemoved((author.authorName).toString()));
        if (context.mounted) onRemoved?.call();
    }
  } catch (e) {
    toast(context.l10n.adminActionFailed((e).toString()));
  }
}
