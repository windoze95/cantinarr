import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/theme/app_theme.dart';
import '../data/request_settings_service.dart';
import '../../request/data/request_service.dart';

/// Admin screen for editing the global media-request defaults.
class RequestSettingsScreen extends ConsumerStatefulWidget {
  const RequestSettingsScreen({super.key});

  @override
  ConsumerState<RequestSettingsScreen> createState() =>
      _RequestSettingsScreenState();
}

class _RequestSettingsScreenState
    extends ConsumerState<RequestSettingsScreen> {
  late final RequestSettingsService _service;

  AdminRequestSettings? _admin;
  GlobalRequestSettings? _edited;
  bool _isLoading = true;
  String? _error;
  bool _saving = false;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      _service = RequestSettingsService(
        backendDio: ref.read(backendClientProvider),
      );
      _load();
    });
  }

  String _friendlyError(Object e) {
    final m = RegExp(r'"error":"([^"]+)"').firstMatch(e.toString());
    return m != null ? m.group(1)! : 'Something went wrong';
  }

  Future<void> _load() async {
    setState(() {
      _isLoading = _admin == null;
      _error = null;
    });
    try {
      final admin = await _service.getAdminSettings();
      if (!mounted) return;
      setState(() {
        _admin = admin;
        _edited = admin.settings;
        _isLoading = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _error = e.toString();
        _isLoading = false;
      });
    }
  }

  Future<void> _save() async {
    final edited = _edited;
    if (edited == null || _saving) return;
    setState(() => _saving = true);
    try {
      final admin = await _service.updateGlobalSettings(edited);
      if (!mounted) return;
      setState(() {
        _admin = admin;
        _edited = admin.settings;
        _saving = false;
      });
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Saved')),
      );
    } catch (e) {
      if (!mounted) return;
      setState(() => _saving = false);
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(_friendlyError(e))),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Request Defaults')),
      body: _isLoading
          ? const Center(
              child: CircularProgressIndicator(color: AppTheme.accent))
          : (_admin == null || _edited == null)
              ? Center(
                  child: Padding(
                    padding: const EdgeInsets.all(24),
                    child: Column(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        Text(_error ?? 'Something went wrong',
                            style: const TextStyle(color: AppTheme.error),
                            textAlign: TextAlign.center),
                        const SizedBox(height: 12),
                        ElevatedButton(
                            onPressed: _load, child: const Text('Retry')),
                      ],
                    ),
                  ),
                )
              : _buildBody(_admin!, _edited!),
    );
  }

  Widget _buildBody(AdminRequestSettings admin, GlobalRequestSettings edited) {
    return ListView(
      padding: const EdgeInsets.symmetric(vertical: 8),
      children: [
        const Padding(
          padding: EdgeInsets.fromLTRB(16, 8, 16, 4),
          child: Text(
            'These defaults apply to every user unless overridden per user. '
            'Changes take effect on new requests.',
            style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
          ),
        ),
        const _SectionLabel('Approval'),
        SwitchListTile(
          value: edited.requireApproval,
          activeThumbColor: AppTheme.accent,
          onChanged: (v) => setState(
              () => _edited = edited.copyWith(requireApproval: v)),
          title: const Text(
            'Require approval for new requests',
            style: TextStyle(
                color: AppTheme.textPrimary, fontWeight: FontWeight.w500),
          ),
          subtitle: const Text(
            'New requests wait in a queue for an admin to approve.',
            style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
          ),
        ),
        const _SectionLabel('Seasons'),
        SwitchListTile(
          value: edited.allowSeasonChoice,
          activeThumbColor: AppTheme.accent,
          onChanged: (v) => setState(
              () => _edited = edited.copyWith(allowSeasonChoice: v)),
          title: const Text(
            'Let users choose seasons',
            style: TextStyle(
                color: AppTheme.textPrimary, fontWeight: FontWeight.w500),
          ),
          subtitle: const Text(
            'Allow users to pick which seasons to request for a show.',
            style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
          ),
        ),
        ListTile(
          title: const Text(
            'Default season scope',
            style: TextStyle(
                color: AppTheme.textPrimary, fontWeight: FontWeight.w500),
          ),
          trailing: DropdownButton<String>(
            value: edited.defaultSeasonScope,
            dropdownColor: AppTheme.surface,
            underline: const SizedBox.shrink(),
            style: const TextStyle(color: AppTheme.textPrimary, fontSize: 14),
            items: [
              for (final c in SeasonScope.choices)
                DropdownMenuItem<String>(
                  value: c.value,
                  child: Text(c.label),
                ),
            ],
            onChanged: (v) {
              if (v == null) return;
              setState(
                  () => _edited = edited.copyWith(defaultSeasonScope: v));
            },
          ),
        ),
        const _SectionLabel('Quality'),
        SwitchListTile(
          value: edited.allowQualityChoice,
          activeThumbColor: AppTheme.accent,
          onChanged: (v) => setState(
              () => _edited = edited.copyWith(allowQualityChoice: v)),
          title: const Text(
            'Let users choose quality',
            style: TextStyle(
                color: AppTheme.textPrimary, fontWeight: FontWeight.w500),
          ),
          subtitle: const Text(
            'Off by default. When off, users get the default profile below.',
            style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
          ),
        ),
        _qualityTile(
          label: 'Default Radarr quality',
          value: edited.defaultQualityRadarr,
          profiles: admin.radarrProfiles,
          onChanged: (v) => setState(
              () => _edited = edited.copyWith(defaultQualityRadarr: v)),
        ),
        _qualityTile(
          label: 'Default Sonarr quality',
          value: edited.defaultQualitySonarr,
          profiles: admin.sonarrProfiles,
          onChanged: (v) => setState(
              () => _edited = edited.copyWith(defaultQualitySonarr: v)),
        ),
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 24, 16, 16),
          child: SizedBox(
            width: double.infinity,
            child: ElevatedButton(
              style: ElevatedButton.styleFrom(
                backgroundColor: AppTheme.accent,
                foregroundColor: Colors.white,
                padding: const EdgeInsets.symmetric(vertical: 14),
              ),
              onPressed: _saving ? null : _save,
              child: _saving
                  ? const SizedBox(
                      width: 20,
                      height: 20,
                      child: CircularProgressIndicator(
                        color: Colors.white,
                        strokeWidth: 2,
                      ),
                    )
                  : const Text('Save'),
            ),
          ),
        ),
      ],
    );
  }

  Widget _qualityTile({
    required String label,
    required int value,
    required List<QualityProfile> profiles,
    required ValueChanged<int> onChanged,
  }) {
    // Guard against a stored value that no longer matches a known profile.
    final hasValue =
        value == 0 || profiles.any((p) => p.id == value);
    return ListTile(
      title: Text(
        label,
        style: const TextStyle(
            color: AppTheme.textPrimary, fontWeight: FontWeight.w500),
      ),
      trailing: DropdownButton<int>(
        value: hasValue ? value : 0,
        dropdownColor: AppTheme.surface,
        underline: const SizedBox.shrink(),
        style: const TextStyle(color: AppTheme.textPrimary, fontSize: 14),
        items: [
          const DropdownMenuItem<int>(
            value: 0,
            child: Text('Server default'),
          ),
          for (final p in profiles)
            DropdownMenuItem<int>(
              value: p.id,
              child: Text(p.name),
            ),
        ],
        onChanged: (v) {
          if (v == null) return;
          onChanged(v);
        },
      ),
    );
  }
}

class _SectionLabel extends StatelessWidget {
  final String text;
  const _SectionLabel(this.text);

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 16, 16, 4),
      child: Text(
        text.toUpperCase(),
        style: const TextStyle(
          color: AppTheme.accent,
          fontSize: 12,
          fontWeight: FontWeight.w600,
          letterSpacing: 0.5,
        ),
      ),
    );
  }
}
