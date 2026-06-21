import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/theme/app_theme.dart';
import '../data/request_settings_service.dart';
import '../../request/data/request_service.dart';

/// Admin screen for editing one user's per-user request overrides. A null
/// value for any option means "inherit the global default".
class UserRequestSettingsScreen extends ConsumerStatefulWidget {
  const UserRequestSettingsScreen({
    super.key,
    required this.userId,
    required this.username,
  });

  final int userId;
  final String username;

  @override
  ConsumerState<UserRequestSettingsScreen> createState() =>
      _UserRequestSettingsScreenState();
}

class _UserRequestSettingsScreenState
    extends ConsumerState<UserRequestSettingsScreen> {
  late final RequestSettingsService _service;

  bool _isLoading = true;
  String? _error;
  bool _saving = false;

  GlobalRequestSettings? _global;
  List<QualityProfile> _radarrProfiles = const [];
  List<QualityProfile> _sonarrProfiles = const [];

  // Mutable working fields mirroring UserRequestSettings (null = inherit).
  bool? _requireApproval;
  bool? _allowSeasonChoice;
  String? _seasonScope;
  bool? _allowQualityChoice;
  int? _qualityRadarr;
  int? _qualitySonarr;

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
      _isLoading = true;
      _error = null;
    });
    try {
      final user = await _service.getUserSettings(widget.userId);
      final admin = await _service.getAdminSettings();
      if (!mounted) return;
      setState(() {
        _global = admin.settings;
        _radarrProfiles = admin.radarrProfiles;
        _sonarrProfiles = admin.sonarrProfiles;
        _requireApproval = user.requireApproval;
        _allowSeasonChoice = user.allowSeasonChoice;
        _seasonScope = user.seasonScope;
        _allowQualityChoice = user.allowQualityChoice;
        _qualityRadarr = user.qualityProfileRadarr;
        _qualitySonarr = user.qualityProfileSonarr;
        _isLoading = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _error = _friendlyError(e);
        _isLoading = false;
      });
    }
  }

  Future<void> _save() async {
    if (_saving) return;
    setState(() => _saving = true);
    try {
      await _service.updateUserSettings(
        widget.userId,
        UserRequestSettings(
          requireApproval: _requireApproval,
          allowSeasonChoice: _allowSeasonChoice,
          seasonScope: _seasonScope,
          allowQualityChoice: _allowQualityChoice,
          qualityProfileRadarr: _qualityRadarr,
          qualityProfileSonarr: _qualitySonarr,
        ),
      );
      if (!mounted) return;
      setState(() => _saving = false);
      ScaffoldMessenger.of(context)
          .showSnackBar(const SnackBar(content: Text('Saved')));
    } catch (e) {
      if (!mounted) return;
      setState(() => _saving = false);
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text(_friendlyError(e))));
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: Text('Request Settings — ${widget.username}')),
      body: _isLoading
          ? const Center(
              child: CircularProgressIndicator(color: AppTheme.accent))
          : _error != null && _global == null
              ? Center(
                  child: Padding(
                    padding: const EdgeInsets.all(24),
                    child: Column(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        Text(_error!,
                            style: const TextStyle(color: AppTheme.error),
                            textAlign: TextAlign.center),
                        const SizedBox(height: 12),
                        ElevatedButton(
                            onPressed: _load, child: const Text('Retry')),
                      ],
                    ),
                  ),
                )
              : _buildBody(),
    );
  }

  Widget _buildBody() {
    final global = _global!;
    return ListView(
      padding: const EdgeInsets.symmetric(vertical: 8),
      children: [
        const Padding(
          padding: EdgeInsets.fromLTRB(16, 8, 16, 12),
          child: Text(
            'Override the global request defaults for this user. Inherit keeps '
            'the global default.',
            style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
          ),
        ),
        _TriBool(
          title: 'Require approval',
          subtitle: 'Requests from this user must be approved before being sent.',
          value: _requireApproval,
          inheritedDefault: global.requireApproval,
          onChanged: (v) => setState(() => _requireApproval = v),
        ),
        const Divider(color: AppTheme.border),
        _TriBool(
          title: 'Allow season choice',
          subtitle: 'Let this user pick which seasons to request for TV.',
          value: _allowSeasonChoice,
          inheritedDefault: global.allowSeasonChoice,
          onChanged: (v) => setState(() => _allowSeasonChoice = v),
        ),
        _seasonScopeField(global),
        const Divider(color: AppTheme.border),
        _TriBool(
          title: 'Allow quality choice',
          subtitle: 'Let this user pick a quality profile for requests.',
          value: _allowQualityChoice,
          inheritedDefault: global.allowQualityChoice,
          onChanged: (v) => setState(() => _allowQualityChoice = v),
        ),
        _qualityField(
          label: 'Radarr quality',
          profiles: _radarrProfiles,
          value: _qualityRadarr,
          onChanged: (v) => setState(() => _qualityRadarr = v),
        ),
        _qualityField(
          label: 'Sonarr quality',
          profiles: _sonarrProfiles,
          value: _qualitySonarr,
          onChanged: (v) => setState(() => _qualitySonarr = v),
        ),
        const SizedBox(height: 24),
        Padding(
          padding: const EdgeInsets.symmetric(horizontal: 16),
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
                          strokeWidth: 2, color: Colors.white),
                    )
                  : const Text('Save'),
            ),
          ),
        ),
        const SizedBox(height: 32),
      ],
    );
  }

  Widget _seasonScopeField(GlobalRequestSettings global) {
    final inheritedLabel = SeasonScope.labelFor(global.defaultSeasonScope);
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 4, 16, 12),
      child: DropdownButtonFormField<String?>(
        initialValue: _seasonScope,
        isExpanded: true,
        dropdownColor: AppTheme.surfaceVariant,
        decoration: const InputDecoration(
          labelText: 'Default season scope',
          labelStyle: TextStyle(color: AppTheme.textSecondary),
          enabledBorder: OutlineInputBorder(
            borderSide: BorderSide(color: AppTheme.border),
          ),
          focusedBorder: OutlineInputBorder(
            borderSide: BorderSide(color: AppTheme.accent),
          ),
        ),
        style: const TextStyle(color: AppTheme.textPrimary),
        items: [
          DropdownMenuItem<String?>(
            value: null,
            child: Text('Inherit ($inheritedLabel)',
                style: const TextStyle(color: AppTheme.textSecondary)),
          ),
          ...SeasonScope.choices.map(
            (c) => DropdownMenuItem<String?>(
              value: c.value,
              child: Text(c.label),
            ),
          ),
        ],
        onChanged: (v) => setState(() => _seasonScope = v),
      ),
    );
  }

  Widget _qualityField({
    required String label,
    required List<QualityProfile> profiles,
    required int? value,
    required ValueChanged<int?> onChanged,
  }) {
    // Guard against a stored id that's no longer in the profile list.
    final hasValue = value != null && profiles.any((p) => p.id == value);
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 4, 16, 12),
      child: DropdownButtonFormField<int?>(
        initialValue: hasValue ? value : null,
        isExpanded: true,
        dropdownColor: AppTheme.surfaceVariant,
        decoration: InputDecoration(
          labelText: label,
          labelStyle: const TextStyle(color: AppTheme.textSecondary),
          enabledBorder: const OutlineInputBorder(
            borderSide: BorderSide(color: AppTheme.border),
          ),
          focusedBorder: const OutlineInputBorder(
            borderSide: BorderSide(color: AppTheme.accent),
          ),
        ),
        style: const TextStyle(color: AppTheme.textPrimary),
        items: [
          const DropdownMenuItem<int?>(
            value: null,
            child: Text('Inherit',
                style: TextStyle(color: AppTheme.textSecondary)),
          ),
          ...profiles.map(
            (p) => DropdownMenuItem<int?>(
              value: p.id,
              child: Text(p.name),
            ),
          ),
        ],
        onChanged: onChanged,
      ),
    );
  }
}

/// A three-way (inherit / on / off) selector backed by a nullable boolean.
class _TriBool extends StatelessWidget {
  final String title;
  final String? subtitle;
  final bool? value;
  final bool inheritedDefault;
  final ValueChanged<bool?> onChanged;

  const _TriBool({
    required this.title,
    this.subtitle,
    required this.value,
    required this.inheritedDefault,
    required this.onChanged,
  });

  @override
  Widget build(BuildContext context) {
    final inheritLabel = 'Inherit (${inheritedDefault ? 'On' : 'Off'})';
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 8),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            title,
            style: const TextStyle(
              color: AppTheme.textPrimary,
              fontWeight: FontWeight.w600,
            ),
          ),
          if (subtitle != null) ...[
            const SizedBox(height: 2),
            Text(
              subtitle!,
              style:
                  const TextStyle(color: AppTheme.textSecondary, fontSize: 13),
            ),
          ],
          const SizedBox(height: 8),
          Wrap(
            spacing: 8,
            children: [
              _chip(inheritLabel, value == null, () => onChanged(null)),
              _chip('On', value == true, () => onChanged(true)),
              _chip('Off', value == false, () => onChanged(false)),
            ],
          ),
        ],
      ),
    );
  }

  Widget _chip(String label, bool selected, VoidCallback onTap) {
    return ChoiceChip(
      label: Text(label),
      selected: selected,
      onSelected: (_) => onTap(),
      backgroundColor: AppTheme.surfaceVariant,
      selectedColor: AppTheme.accent,
      side: const BorderSide(color: AppTheme.border),
      labelStyle: TextStyle(
        color: selected ? Colors.white : AppTheme.textSecondary,
        fontWeight: selected ? FontWeight.w600 : FontWeight.w400,
      ),
    );
  }
}
