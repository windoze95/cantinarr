import 'package:flutter/material.dart';
import '../../../core/network/library_settings_service.dart';
import '../../../core/widgets/action_sheet.dart';
import '../../../core/widgets/library_actions.dart';
import '../../../core/widgets/library_settings_screen.dart';
import '../../../navigation/ambient_page_route.dart';
import '../data/lidarr_api_service.dart';
import '../data/lidarr_models.dart';
import 'lidarr_releases_screen.dart';

Future<void> showArtistActions(BuildContext context, {
  required LidarrApiService service,
  required LibrarySettingsService settings,
  required String instanceId,
  required LidarrArtist artist,
  LibraryAction? selectedAction,
  VoidCallback? onChanged,
  VoidCallback? onRemoved,
}) async {
  final action = selectedAction ?? await showActionSheet<LibraryAction>(
    context, title: artist.artistName,
    actions: libraryActions('artist', monitored: artist.monitored),
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
        await service.searchArtist(artist.id);
        toast('Search queued for ${artist.artistName}');
      case LibraryAction.interactive:
        final records = (await service.getAlbums(artistId: artist.id))
            .where((record) => record.artistId == artist.id).toList()
          ..sort((a, b) => a.title.toLowerCase().compareTo(b.title.toLowerCase()));
        if (!context.mounted) return;
        if (records.isEmpty) { toast('No albums in this artist’s library'); return; }
        final id = await showActionSheet<int>(context,
          title: 'Select album', actions: [
            for (final record in records) SheetAction(record.id, Icons.album, record.title),
          ]);
        if (id == null || !context.mounted) return;
        final selected = records.firstWhere((r) => r.id == id);
        await Navigator.of(context, rootNavigator: true).push(AmbientPageRoute(
          builder: (_) => LidarrReleasesScreen(instanceId: instanceId,
            albumId: id, albumTitle: selected.title)));
      case LibraryAction.edit:
      case LibraryAction.monitoring:
        if (action == LibraryAction.monitoring) {
          await settings.update({'monitored': !artist.monitored});
          toast(artist.monitored ? 'Stopped monitoring ${artist.artistName}' : 'Monitoring ${artist.artistName}');
          if (context.mounted) onChanged?.call();
          return;
        }
        final saved = await Navigator.of(context, rootNavigator: true).push<bool>(AmbientPageRoute(
          builder: (_) => LibrarySettingsScreen(service: settings, title: artist.artistName,
            monitoringOnly: action == LibraryAction.monitoring)));
        if (saved == true && context.mounted) { toast('Settings saved'); onChanged?.call(); }
      case LibraryAction.refresh:
        await service.refreshArtist(artist.id);
        toast('Refresh queued for ${artist.artistName}');
        if (context.mounted) onChanged?.call();
      case LibraryAction.rescan:
        await service.rescanArtist(artist.id);
        toast('Rescan queued for ${artist.artistName}');
        if (context.mounted) onChanged?.call();
      case LibraryAction.remove:
        final deleteFiles = await confirmLibraryRemoval(context, title: artist.artistName,
          noun: 'artist', service: 'Lidarr');
        if (deleteFiles == null) return;
        await service.deleteArtist(artist.id, deleteFiles: deleteFiles);
        toast('Removed ${artist.artistName}');
        if (context.mounted) onRemoved?.call();
    }
  } catch (e) { toast('Action failed: $e'); }
}
