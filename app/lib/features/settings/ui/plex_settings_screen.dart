import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:url_launcher/url_launcher.dart';
import '../../../core/theme/app_theme.dart';
import '../data/plex_admin_service.dart';

/// Admin screen for the Plex integration: link a Plex account (PIN flow),
/// pick the server and libraries invites share, and turn on auto-invite so a
/// user sharing their Plex email gets invited with zero admin taps.
class PlexSettingsScreen extends ConsumerStatefulWidget {
  const PlexSettingsScreen({super.key});

  @override
  ConsumerState<PlexSettingsScreen> createState() => _PlexSettingsScreenState();
}

class _PlexSettingsScreenState extends ConsumerState<PlexSettingsScreen> {
  bool _loading = true;
  String? _error;
  PlexStatus? _status;

  // PIN link flow state.
  bool _linking = false;
  int? _pinId;
  String? _linkUrl;
  Timer? _pollTimer;

  // Invite configuration (edited locally, saved with the Save button).
  List<PlexServer> _servers = const [];
  List<PlexLibrary> _libraries = const [];
  bool _librariesLoading = false;
  String _machineId = '';
  String _serverName = '';
  Set<int> _selectedLibraryIds = {};
  bool _autoInvite = false;
  bool _saving = false;

  PlexAdminService get _service => ref.read(plexAdminServiceProvider);

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void dispose() {
    _pollTimer?.cancel();
    super.dispose();
  }

  Future<void> _load() async {
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final status = await _service.status();
      if (!mounted) return;
      setState(() {
        _status = status;
        _machineId = status.machineIdentifier;
        _serverName = status.serverName;
        _selectedLibraryIds = status.librarySectionIds.toSet();
        _autoInvite = status.autoInvite;
        _loading = false;
      });
      if (status.linked) {
        await _loadServers();
        if (status.machineIdentifier.isNotEmpty) {
          await _loadLibraries(status.machineIdentifier);
        }
      }
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _error = 'Failed to load Plex settings';
        _loading = false;
      });
    }
  }

  Future<void> _loadServers() async {
    try {
      final servers = await _service.servers();
      if (mounted) setState(() => _servers = servers);
    } catch (_) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(const SnackBar(
            content: Text('Could not load your Plex servers — try again')));
      }
    }
  }

  Future<void> _loadLibraries(String machineId) async {
    setState(() {
      _librariesLoading = true;
      _libraries = const [];
    });
    try {
      final libs = await _service.libraries(machineId);
      if (mounted) setState(() => _libraries = libs);
    } catch (_) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(const SnackBar(
            content: Text('Could not load libraries for that server')));
      }
    } finally {
      if (mounted) setState(() => _librariesLoading = false);
    }
  }

  Future<void> _beginLink() async {
    try {
      final start = await _service.beginLink();
      if (!mounted) return;
      setState(() {
        _linking = true;
        _pinId = start.pinId;
        _linkUrl = start.url;
      });
      await launchUrl(Uri.parse(start.url),
          mode: LaunchMode.externalApplication);
      // Poll while the admin approves in the browser; the PIN expires
      // server-side so a forgotten screen just times out quietly.
      _pollTimer?.cancel();
      _pollTimer = Timer.periodic(
        const Duration(seconds: 3),
        (_) => _checkLink(silent: true),
      );
    } catch (_) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(const SnackBar(
            content: Text('Could not reach plex.tv — try again')));
      }
    }
  }

  Future<void> _checkLink({bool silent = false}) async {
    final pinId = _pinId;
    if (pinId == null) return;
    try {
      final linked = await _service.checkLink(pinId);
      if (!linked || !mounted) return;
      _pollTimer?.cancel();
      setState(() {
        _linking = false;
        _pinId = null;
        _linkUrl = null;
      });
      ref.invalidate(plexInviteConfiguredProvider);
      ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('Plex account linked!')));
      await _load();
    } catch (_) {
      if (!silent && mounted) {
        ScaffoldMessenger.of(context).showSnackBar(const SnackBar(
            content: Text('Not approved yet — finish signing in on plex.tv')));
      }
    }
  }

  void _cancelLink() {
    _pollTimer?.cancel();
    setState(() {
      _linking = false;
      _pinId = null;
      _linkUrl = null;
    });
  }

  Future<void> _unlink() async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (dialogContext) => AlertDialog(
        title: const Text('Unlink Plex account?'),
        content: const Text(
            'Cantinarr forgets the Plex token and invite settings. Invites '
            'already sent are not affected.'),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(dialogContext).pop(false),
            child: const Text('Cancel'),
          ),
          ElevatedButton(
            onPressed: () => Navigator.of(dialogContext).pop(true),
            child: const Text('Unlink'),
          ),
        ],
      ),
    );
    if (confirmed != true) return;
    try {
      await _service.unlink();
      ref.invalidate(plexInviteConfiguredProvider);
      await _load();
    } catch (_) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
            const SnackBar(content: Text('Failed to unlink — try again')));
      }
    }
  }

  Future<void> _save() async {
    if (_machineId.isEmpty) {
      ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('Pick a server first')));
      return;
    }
    setState(() => _saving = true);
    try {
      final status = await _service.updateSettings(
        machineIdentifier: _machineId,
        serverName: _serverName,
        librarySectionIds: _selectedLibraryIds.toList()..sort(),
        autoInvite: _autoInvite,
      );
      ref.invalidate(plexInviteConfiguredProvider);
      if (!mounted) return;
      setState(() => _status = status);
      ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('Plex invite settings saved')));
    } catch (_) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
            const SnackBar(content: Text('Failed to save — try again')));
      }
    } finally {
      if (mounted) setState(() => _saving = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Plex Invites')),
      body: _loading
          ? const Center(
              child: CircularProgressIndicator(color: AppTheme.accent))
          : _error != null
              ? Center(
                  child: Column(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      Text(_error!,
                          style: const TextStyle(color: AppTheme.error)),
                      const SizedBox(height: 12),
                      ElevatedButton(
                          onPressed: _load, child: const Text('Retry')),
                    ],
                  ),
                )
              : _buildBody(),
    );
  }

  Widget _buildBody() {
    final status = _status!;
    return ListView(
      padding: const EdgeInsets.symmetric(vertical: 8),
      children: [
        const Padding(
          padding: EdgeInsets.fromLTRB(16, 8, 16, 12),
          child: Text(
            'Link your Plex account so Cantinarr can send server invites '
            'itself: one tap from the Users screen, or automatically the '
            'moment someone shares their Plex email.',
            style: TextStyle(
                color: AppTheme.textSecondary, fontSize: 13, height: 1.4),
          ),
        ),
        if (!status.linked) ..._buildUnlinked() else ..._buildLinked(status),
        const SizedBox(height: 32),
      ],
    );
  }

  List<Widget> _buildUnlinked() {
    if (_linking) {
      return [
        const ListTile(
          leading: SizedBox(
            width: 24,
            height: 24,
            child: CircularProgressIndicator(
                strokeWidth: 2, color: AppTheme.accent),
          ),
          title: Text('Waiting for approval…',
              style: TextStyle(color: AppTheme.textPrimary)),
          subtitle: Text(
              'Sign in on the plex.tv page that just opened and approve the link.',
              style: TextStyle(color: AppTheme.textSecondary, fontSize: 13)),
        ),
        Padding(
          padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
          child: Wrap(
            spacing: 12,
            runSpacing: 8,
            children: [
              OutlinedButton(
                onPressed: () => _checkLink(),
                child: const Text("I've approved — check now"),
              ),
              if (_linkUrl != null)
                OutlinedButton(
                  onPressed: () => launchUrl(Uri.parse(_linkUrl!),
                      mode: LaunchMode.externalApplication),
                  child: const Text('Reopen plex.tv'),
                ),
              TextButton(
                onPressed: _cancelLink,
                child: const Text('Cancel'),
              ),
            ],
          ),
        ),
      ];
    }
    return [
      Padding(
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
        child: ElevatedButton.icon(
          onPressed: _beginLink,
          icon: const Icon(Icons.link, size: 18),
          label: const Text('Link Plex account'),
        ),
      ),
    ];
  }

  List<Widget> _buildLinked(PlexStatus status) {
    return [
      const _SectionHeader(title: 'Account'),
      ListTile(
        leading: const Icon(Icons.check_circle, color: AppTheme.available),
        title: Text(status.account.isEmpty ? 'Linked' : status.account,
            style: const TextStyle(
                color: AppTheme.textPrimary, fontWeight: FontWeight.w500)),
        subtitle: const Text('Plex account linked',
            style: TextStyle(color: AppTheme.textSecondary, fontSize: 13)),
        trailing: TextButton(
          onPressed: _unlink,
          child: const Text('Unlink', style: TextStyle(color: AppTheme.error)),
        ),
      ),
      const SizedBox(height: 8),
      const _SectionHeader(title: 'Server'),
      if (_servers.isEmpty)
        const ListTile(
          leading: Icon(Icons.dns_outlined, color: AppTheme.textSecondary),
          title: Text('No owned servers found',
              style: TextStyle(color: AppTheme.textPrimary)),
          subtitle: Text(
              'The linked account must own the Plex Media Server it invites to.',
              style: TextStyle(color: AppTheme.textSecondary, fontSize: 13)),
        )
      else
        ..._servers.map((s) {
          final selected = s.machineIdentifier == _machineId;
          return ListTile(
            leading: Icon(
              selected
                  ? Icons.radio_button_checked
                  : Icons.radio_button_unchecked,
              color: selected ? AppTheme.accent : AppTheme.textSecondary,
            ),
            title: Text(s.name,
                style: const TextStyle(
                    color: AppTheme.textPrimary, fontWeight: FontWeight.w500)),
            onTap: selected
                ? null
                : () {
                    setState(() {
                      _machineId = s.machineIdentifier;
                      _serverName = s.name;
                      _selectedLibraryIds = {};
                    });
                    _loadLibraries(s.machineIdentifier);
                  },
          );
        }),
      if (_machineId.isNotEmpty) ...[
        const SizedBox(height: 8),
        const _SectionHeader(title: 'Libraries'),
        const Padding(
          padding: EdgeInsets.fromLTRB(16, 0, 16, 8),
          child: Text(
            'Pick the libraries invites share. Leave all unchecked to share '
            'every library.',
            style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
          ),
        ),
        if (_librariesLoading)
          const Padding(
            padding: EdgeInsets.all(16),
            child: Center(
              child: SizedBox(
                width: 24,
                height: 24,
                child: CircularProgressIndicator(
                    strokeWidth: 2, color: AppTheme.accent),
              ),
            ),
          )
        else
          ..._libraries.map((lib) => CheckboxListTile(
                value: _selectedLibraryIds.contains(lib.id),
                onChanged: (checked) {
                  setState(() {
                    if (checked == true) {
                      _selectedLibraryIds.add(lib.id);
                    } else {
                      _selectedLibraryIds.remove(lib.id);
                    }
                  });
                },
                activeColor: AppTheme.accent,
                title: Text(lib.title,
                    style: const TextStyle(color: AppTheme.textPrimary)),
                subtitle: Text(lib.type,
                    style: const TextStyle(
                        color: AppTheme.textSecondary, fontSize: 13)),
              )),
      ],
      const SizedBox(height: 8),
      const _SectionHeader(title: 'Invites'),
      SwitchListTile(
        value: _autoInvite,
        onChanged: (v) => setState(() => _autoInvite = v),
        activeThumbColor: AppTheme.accent,
        title: const Text('Auto-invite',
            style: TextStyle(
                color: AppTheme.textPrimary, fontWeight: FontWeight.w500)),
        subtitle: const Text(
            'Send the invite automatically when someone shares their Plex email',
            style: TextStyle(color: AppTheme.textSecondary, fontSize: 13)),
      ),
      Padding(
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
        child: ElevatedButton(
          onPressed: _saving ? null : _save,
          child: _saving
              ? const SizedBox(
                  width: 18,
                  height: 18,
                  child: CircularProgressIndicator(strokeWidth: 2),
                )
              : const Text('Save'),
        ),
      ),
    ];
  }
}

class _SectionHeader extends StatelessWidget {
  final String title;
  const _SectionHeader({required this.title});

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
      child: Text(
        title.toUpperCase(),
        style: const TextStyle(
          color: AppTheme.accent,
          fontSize: 12,
          fontWeight: FontWeight.w700,
          letterSpacing: 1.2,
        ),
      ),
    );
  }
}
