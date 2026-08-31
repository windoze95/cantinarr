import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/models/backend_connection.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/app_sheet.dart';
import '../../auth/logic/auth_provider.dart';
import '../data/media_access_service.dart';

/// Opens the admin import for one media server: pick accounts the server
/// lists, and each becomes a Cantinarr user (named after it), granted the
/// server and linked to the account, with a connect link to hand out.
/// Resolves to the number of accounts imported, or null when dismissed
/// before importing.
Future<int?> showMediaServerImportSheet(
  BuildContext context, {
  required ServiceInstance server,
  required Map<String, int> existingUsers,
  required Map<String, String> linkedTo,
}) =>
    showAppSheet<int>(
      context,
      builder: (_) => MediaServerImportSheet(
        server: server,
        existingUsers: existingUsers,
        linkedTo: linkedTo,
      ),
    );

/// Lists the accounts on the server with what importing each would do
/// (a new user, an existing user of that name, or nothing because it is
/// already linked), imports the picked ones, and shows each outcome with its
/// connect link. The admin's pick is the mapping; nothing on the server
/// changes but a switched-off account, which is switched on with its access.
class MediaServerImportSheet extends ConsumerStatefulWidget {
  final ServiceInstance server;

  /// Cantinarr usernames that already exist, to their ids: an account of the
  /// same name is attached to that user rather than a new one.
  final Map<String, int> existingUsers;

  /// Remote account ids already linked, to the Cantinarr username holding
  /// them: those rows are shown but cannot be picked.
  final Map<String, String> linkedTo;

  const MediaServerImportSheet({
    super.key,
    required this.server,
    required this.existingUsers,
    required this.linkedTo,
  });

  @override
  ConsumerState<MediaServerImportSheet> createState() =>
      _MediaServerImportSheetState();
}

class _MediaServerImportSheetState
    extends ConsumerState<MediaServerImportSheet> {
  List<RemoteMediaServerUser>? _users;
  bool _failed = false;
  bool _busy = false;
  final Set<String> _picked = {};
  List<MediaServerImportResult>? _results;

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
          .listRemoteUsers(widget.server.id);
      if (!mounted) return;
      setState(() => _users = users);
    } catch (_) {
      if (!mounted) return;
      setState(() => _failed = true);
    }
  }

  bool _pickable(RemoteMediaServerUser user) =>
      !widget.linkedTo.containsKey(user.id);

  /// What importing this account would do, said before it happens.
  String? _subtitle(RemoteMediaServerUser user) {
    final holder = widget.linkedTo[user.id];
    if (holder != null) return 'Already linked to $holder';
    final parts = <String>[
      if (user.isAdministrator) 'Administrator',
      if (user.isDisabled) 'Turned off on the server',
      if (user.pending) 'Invite pending',
      if (widget.existingUsers.containsKey(user.name))
        'Existing Cantinarr user ${user.name}'
      else
        'New Cantinarr user ${user.name}',
    ];
    return parts.join(' · ');
  }

  void _selectAll() {
    setState(() {
      for (final user in _users ?? const <RemoteMediaServerUser>[]) {
        if (_pickable(user) && !user.isAdministrator) _picked.add(user.id);
      }
    });
  }

  Future<void> _import() async {
    final conn = ref.read(authProvider).valueOrNull?.connection;
    if (conn == null || _picked.isEmpty) return;
    setState(() => _busy = true);
    try {
      final results = await ref.read(mediaAccessServiceProvider).importAccounts(
            instanceId: widget.server.id,
            remoteUserIds: _picked.toList(),
            serverUrl: conn.serverUrl,
          );
      if (!mounted) return;
      setState(() {
        _results = results;
        _busy = false;
      });
    } catch (_) {
      if (!mounted) return;
      setState(() => _busy = false);
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(
        content: Text("Couldn't import from ${widget.server.name}. Try again "
            'in a moment.'),
      ));
    }
  }

  Future<void> _copy(String text, String message) async {
    await Clipboard.setData(ClipboardData(text: text));
    if (!mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text(message)),
    );
  }

  /// One row's outcome in the admin's words.
  String _outcome(MediaServerImportResult r) {
    switch (r.error) {
      case '':
        return r.created
            ? 'New user ${r.username}, linked'
            : 'Existing user ${r.username}, linked';
      case 'already_linked':
        return 'Already linked to another user';
      case 'not_found':
        return 'Not on the server any more';
      case 'user_has_account':
        return '${r.username} already has an account here';
      case 'user_failed':
        return "Couldn't create the user";
      default:
        return r.username.isEmpty
            ? "Couldn't link the account"
            : "User ${r.username} is there, but the account couldn't be "
                'linked. Link it from Users.';
    }
  }

  @override
  Widget build(BuildContext context) {
    final name = widget.server.name;
    final results = _results;
    return PopScope(
      canPop: !_busy,
      child: AppSheet(
        padding: const EdgeInsets.fromLTRB(
          AppTheme.spaceXl,
          0,
          AppTheme.spaceXl,
          AppTheme.spaceXl,
        ),
        child: results == null ? _buildPicker(name) : _buildResults(results),
      ),
    );
  }

  Widget _buildPicker(String name) {
    final users = _users;
    final pickable =
        users?.where(_pickable).length ?? 0;
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          'Import from $name',
          style: const TextStyle(
            color: AppTheme.textPrimary,
            fontSize: 20,
            fontWeight: FontWeight.bold,
          ),
        ),
        const SizedBox(height: AppTheme.spaceSm),
        Text(
          'Each picked account gets a Cantinarr user with the same name, '
          'access to $name, and the account linked. Nothing else on $name '
          'changes: an account switched off there is switched back on, '
          'since it gets access.',
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
                  style: const TextStyle(color: AppTheme.error, fontSize: 13),
                ),
              ),
              TextButton(onPressed: _load, child: const Text('Retry')),
            ],
          )
        else if (users == null)
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
        else if (users.isEmpty)
          Text(
            'No accounts on $name.',
            style:
                const TextStyle(color: AppTheme.textSecondary, fontSize: 13),
          )
        else ...[
          for (final user in users)
            CheckboxListTile(
              contentPadding: EdgeInsets.zero,
              controlAffinity: ListTileControlAffinity.leading,
              value: _picked.contains(user.id),
              onChanged: _pickable(user) && !_busy
                  ? (checked) => setState(() {
                        if (checked == true) {
                          _picked.add(user.id);
                        } else {
                          _picked.remove(user.id);
                        }
                      })
                  : null,
              title: Text(user.name,
                  style: const TextStyle(color: AppTheme.textPrimary)),
              subtitle: Text(
                _subtitle(user) ?? '',
                style: TextStyle(
                  color: widget.linkedTo.containsKey(user.id)
                      ? AppTheme.textMuted
                      : AppTheme.textSecondary,
                  fontSize: 12,
                ),
              ),
            ),
          const SizedBox(height: AppTheme.spaceMd),
          Row(
            children: [
              TextButton(
                onPressed: _busy || pickable == 0 ? null : _selectAll,
                child: const Text('Select all'),
              ),
              const Spacer(),
              ElevatedButton(
                onPressed: _busy || _picked.isEmpty ? null : _import,
                child: _busy
                    ? const SizedBox(
                        width: 18,
                        height: 18,
                        child: CircularProgressIndicator(
                            strokeWidth: 2, color: Colors.white),
                      )
                    : Text(switch (_picked.length) {
                        0 => 'Import',
                        1 => 'Import 1 account',
                        final n => 'Import $n accounts',
                      }),
              ),
            ],
          ),
          const SizedBox(height: AppTheme.spaceSm),
          const Text(
            'Select all skips administrator accounts; tick those yourself.',
            style: TextStyle(color: AppTheme.textMuted, fontSize: 12),
          ),
        ],
      ],
    );
  }

  Widget _buildResults(List<MediaServerImportResult> results) {
    final imported = results.where((r) => r.linked).length;
    final links = [
      for (final r in results)
        if (r.link.isNotEmpty) '${r.username}: ${r.link}',
    ];
    final fromApp = results.any((r) => r.originSource == 'app');
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          imported == 1
              ? 'Imported 1 account from ${widget.server.name}'
              : 'Imported $imported accounts from ${widget.server.name}',
          style: const TextStyle(
            color: AppTheme.textPrimary,
            fontSize: 20,
            fontWeight: FontWeight.bold,
          ),
        ),
        const SizedBox(height: AppTheme.spaceSm),
        const Text(
          'Share each new user their link. It signs one device into the app, '
          'once, and expires in 7 days.',
          style: TextStyle(
            color: AppTheme.textSecondary,
            fontSize: 14,
            height: 1.4,
          ),
        ),
        const SizedBox(height: AppTheme.spaceLg),
        for (final r in results)
          ListTile(
            contentPadding: EdgeInsets.zero,
            leading: Icon(
              r.linked ? Icons.check_circle : Icons.error_outline,
              color: r.linked ? AppTheme.available : AppTheme.warning,
            ),
            title: Text(r.remoteUsername.isEmpty ? r.remoteUserId : r.remoteUsername,
                style: const TextStyle(color: AppTheme.textPrimary)),
            subtitle: Text(
              _outcome(r),
              style:
                  const TextStyle(color: AppTheme.textSecondary, fontSize: 12),
            ),
            trailing: r.link.isEmpty
                ? null
                : IconButton(
                    tooltip: 'Copy link for ${r.username}',
                    icon: const Icon(Icons.copy, size: 18),
                    onPressed: () =>
                        _copy(r.link, 'Link for ${r.username} copied'),
                  ),
          ),
        if (fromApp) ...[
          const SizedBox(height: AppTheme.spaceSm),
          const Text(
            'These links use the address your app connects with. If they '
            'are not on your network, set External Address in Settings and '
            'issue new links from Users.',
            style: TextStyle(color: AppTheme.textSecondary, fontSize: 12),
          ),
        ],
        const SizedBox(height: AppTheme.spaceMd),
        Row(
          children: [
            if (links.isNotEmpty)
              TextButton.icon(
                onPressed: () =>
                    _copy(links.join('\n'), 'All links copied'),
                icon: const Icon(Icons.copy_all, size: 18),
                label: const Text('Copy all links'),
              ),
            const Spacer(),
            ElevatedButton(
              onPressed: () => Navigator.of(context).pop(imported),
              child: const Text('Done'),
            ),
          ],
        ),
      ],
    );
  }
}
