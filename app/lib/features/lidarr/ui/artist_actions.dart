import 'package:cantinarr/l10n/l10n.dart';
import 'package:flutter/material.dart';
import '../../../core/network/library_settings_service.dart';
import '../../../core/widgets/action_sheet.dart';
import '../../../core/widgets/library_actions.dart';
import '../../../core/widgets/library_settings_screen.dart';
import '../../../navigation/ambient_page_route.dart';
import '../data/lidarr_api_service.dart';
import '../data/lidarr_models.dart';
import 'lidarr_releases_screen.dart';

Future<void> showArtistActions(
  BuildContext context, {
  required LidarrApiService service,
  required LibrarySettingsService settings,
  required String instanceId,
  required LidarrArtist artist,
  LibraryAction? selectedAction,
  VoidCallback? onChanged,
  VoidCallback? onRemoved,
}) async {
  final action = selectedAction ??
      await showActionSheet<LibraryAction>(
        context,
        title: artist.artistName,
        actions: libraryActions('artist', monitored: artist.monitored),
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
        await service.searchArtist(artist.id);
        toast(
            context.l10n.adminSearchQueuedFor((artist.artistName).toString()));
      case LibraryAction.interactive:
        final records = (await service.getAlbums(artistId: artist.id))
            .where((record) => record.artistId == artist.id)
            .toList()
          ..sort(
              (a, b) => a.title.toLowerCase().compareTo(b.title.toLowerCase()));
        if (!context.mounted) return;
        if (records.isEmpty) {
          toast(context.l10n.adminNoAlbumsInThisArtistSLibrary);
          return;
        }
        final id = await showActionSheet<int>(context,
            title: context.l10n.adminSelectAlbum,
            actions: [
              for (final record in records)
                SheetAction(record.id, Icons.album, record.title),
            ]);
        if (id == null || !context.mounted) return;
        final selected = records.firstWhere((r) => r.id == id);
        await Navigator.of(context, rootNavigator: true).push(AmbientPageRoute(
            builder: (_) => LidarrReleasesScreen(
                instanceId: instanceId,
                albumId: id,
                albumTitle: selected.title)));
      case LibraryAction.edit:
      case LibraryAction.monitoring:
        if (action == LibraryAction.monitoring) {
          await settings.update({'monitored': !artist.monitored});
          toast(artist.monitored
              ? context.l10n
                  .adminStoppedMonitoring2((artist.artistName).toString())
              : context.l10n.adminMonitoring2((artist.artistName).toString()));
          if (context.mounted) onChanged?.call();
          return;
        }
        final saved = await Navigator.of(context, rootNavigator: true)
            .push<bool>(AmbientPageRoute(
                builder: (_) => LibrarySettingsScreen(
                    service: settings,
                    title: artist.artistName,
                    monitoringOnly: action == LibraryAction.monitoring)));
        if (saved == true && context.mounted) {
          toast(context.l10n.adminSettingsSaved);
          onChanged?.call();
        }
      case LibraryAction.refresh:
        await service.refreshArtist(artist.id);
        toast(
            context.l10n.adminRefreshQueuedFor((artist.artistName).toString()));
        if (context.mounted) onChanged?.call();
      case LibraryAction.rescan:
        await service.rescanArtist(artist.id);
        toast(
            context.l10n.adminRescanQueuedFor((artist.artistName).toString()));
        if (context.mounted) onChanged?.call();
      case LibraryAction.remove:
        final deleteFiles = await confirmLibraryRemoval(context,
            title: artist.artistName, noun: 'artist', service: 'Lidarr');
        if (deleteFiles == null) return;
        await service.deleteArtist(artist.id, deleteFiles: deleteFiles);
        toast(context.l10n.adminRemoved((artist.artistName).toString()));
        if (context.mounted) onRemoved?.call();
    }
  } catch (e) {
    toast(context.l10n.adminActionFailed((e).toString()));
  }
}
