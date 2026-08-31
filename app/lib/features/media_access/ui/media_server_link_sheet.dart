import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/app_sheet.dart';
import '../data/media_access_service.dart';

/// Opens the admin picker for an existing account on a media server. Resolves
/// to the chosen account, or null when dismissed.
Future<RemoteMediaServerUser?> showMediaServerLinkSheet(
  BuildContext context, {
  required String instanceId,
  required String instanceName,
  required String serviceType,
  required String username,
}) =>
    showAppSheet<RemoteMediaServerUser>(
      context,
      builder: (_) => MediaServerLinkSheet(
        instanceId: instanceId,
        instanceName: instanceName,
        serviceType: serviceType,
        username: username,
      ),
    );

/// Lists the accounts the media server reports so an admin can say which one
/// belongs to a Cantinarr user (a name collision, or a Jellyseerr migrant).
/// Administrators are listed as such; picking any account only records the
/// link, and an administrator account is never changed afterwards either.
class MediaServerLinkSheet extends ConsumerStatefulWidget {
  final String instanceId;
  final String instanceName;
  final String serviceType;
  final String username;

  const MediaServerLinkSheet({
    super.key,
    required this.instanceId,
    required this.instanceName,
    required this.serviceType,
    required this.username,
  });

  @override
  ConsumerState<MediaServerLinkSheet> createState() =>
      _MediaServerLinkSheetState();
}

class _MediaServerLinkSheetState extends ConsumerState<MediaServerLinkSheet> {
  List<RemoteMediaServerUser>? _users;
  bool _failed = false;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    setState(() {
      _users = null;
      _failed = false;
    });
    try {
      final users = await ref
          .read(mediaAccessServiceProvider)
          .listRemoteUsers(widget.instanceId);
      if (!mounted) return;
      setState(() => _users = users);
    } catch (_) {
      if (!mounted) return;
      setState(() => _failed = true);
    }
  }

  @override
  Widget build(BuildContext context) {
    final name = widget.instanceName;
    final product = mediaServerTypeLabel(widget.serviceType);
    final users = _users;
    final linkable = users;
    return AppSheet(
      padding: const EdgeInsets.fromLTRB(
        AppTheme.spaceXl,
        0,
        AppTheme.spaceXl,
        AppTheme.spaceXl,
      ),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            'Link a $product account',
            style: const TextStyle(
              color: AppTheme.textPrimary,
              fontSize: 20,
              fontWeight: FontWeight.bold,
            ),
          ),
          const SizedBox(height: AppTheme.spaceSm),
          Text(
            'Pick the account on $name that belongs to ${widget.username}. '
            "Linking only records the connection; the account itself isn't "
            'changed.',
            style: const TextStyle(
              color: AppTheme.textSecondary,
              fontSize: 14,
              height: 1.4,
            ),
          ),
          const SizedBox(height: AppTheme.spaceLg),
          if (_failed)
            Row(
              children: [
                Expanded(
                  child: Text(
                    "Couldn't load the accounts on $name.",
                    style:
                        const TextStyle(color: AppTheme.error, fontSize: 13),
                  ),
                ),
                TextButton(onPressed: _load, child: const Text('Retry')),
              ],
            )
          else if (linkable == null)
            const Padding(
              padding: EdgeInsets.symmetric(vertical: 12),
              child: Center(
                child: SizedBox(
                  width: 20,
                  height: 20,
                  child: CircularProgressIndicator(
                      strokeWidth: 2, color: AppTheme.accent),
                ),
              ),
            )
          else if (linkable.isEmpty)
            Text(
              'No accounts to link on $name.',
              style:
                  const TextStyle(color: AppTheme.textSecondary, fontSize: 13),
            )
          else
            for (final user in linkable)
              ListTile(
                contentPadding: EdgeInsets.zero,
                leading: const Icon(Icons.person_outline,
                    color: AppTheme.textSecondary),
                title: Text(user.name,
                    style: const TextStyle(color: AppTheme.textPrimary)),
                subtitle: user.isAdministrator
                    ? const Text('Administrator',
                        style: TextStyle(
                            color: AppTheme.textSecondary, fontSize: 12))
                    : user.isDisabled
                        ? const Text('Turned off on the server',
                            style: TextStyle(
                                color: AppTheme.unavailable, fontSize: 12))
                        : null,
                onTap: () => Navigator.of(context).pop(user),
              ),
          const SizedBox(height: AppTheme.spaceMd),
          const Text(
            'Administrator accounts can be linked; Cantinarr never changes '
            'them.',
            style: TextStyle(color: AppTheme.textMuted, fontSize: 12),
          ),
        ],
      ),
    );
  }
}
