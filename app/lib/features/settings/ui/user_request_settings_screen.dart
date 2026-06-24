import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/models/backend_connection.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/theme/app_theme.dart';
import '../data/request_settings_service.dart';
import '../../auth/logic/auth_provider.dart';
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

  // The user's per-service default-instance overrides, keyed by service type
  // (null = inherit the global default; for chaptarr, null = no access).
  Map<String, String?> _defaultInstances = {};

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
      final defaults = await _service.getUserDefaultInstances(widget.userId);
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
        _defaultInstances = Map<String, String?>.from(defaults);
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
      // Send the override for every service type that has instances so a
      // cleared selection serializes to null (which clears it server-side).
      final defaults = <String, String?>{
        for (final type in _instancesByType().keys)
          type: _defaultInstances[type],
      };
      await _service.updateUserDefaultInstances(widget.userId, defaults);
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
      appBar: AppBar(title: Text('User Settings — ${widget.username}')),
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
          subtitle:
              'Requests from this user must be approved before being sent.',
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
        ..._buildDefaultInstancesSection(),
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

  /// The admin's connection lists every configured instance; group them by
  /// service type (first-seen order) so we can render one dropdown per type.
  /// Service types whose default instance is a per-user "source" override.
  /// Download clients and Tautulli are admin-only infrastructure (not a
  /// per-user content source), so they are excluded from this section.
  static const _sourceServiceTypes = {'radarr', 'sonarr', 'chaptarr'};

  Map<String, List<ServiceInstance>> _instancesByType() {
    final instances =
        ref.read(authProvider).valueOrNull?.connection?.instances ?? const [];
    final grouped = <String, List<ServiceInstance>>{};
    for (final inst in instances) {
      if (!_sourceServiceTypes.contains(inst.serviceType)) continue;
      grouped.putIfAbsent(inst.serviceType, () => []).add(inst);
    }
    return grouped;
  }

  /// A "Default instances" section with one dropdown per service type that has
  /// at least one configured instance.
  List<Widget> _buildDefaultInstancesSection() {
    final grouped = _instancesByType();
    if (grouped.isEmpty) return const [];
    return [
      const Divider(color: AppTheme.border),
      const Padding(
        padding: EdgeInsets.fromLTRB(16, 8, 16, 4),
        child: Text(
          'Default instances',
          style: TextStyle(
            color: AppTheme.textPrimary,
            fontWeight: FontWeight.w600,
          ),
        ),
      ),
      const Padding(
        padding: EdgeInsets.fromLTRB(16, 0, 16, 8),
        child: Text(
          'Pin which instance this user defaults to per service. Chaptarr (Books) '
          'has no global default — choosing an instance grants access.',
          style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
        ),
      ),
      for (final entry in grouped.entries)
        _defaultInstanceField(serviceType: entry.key, instances: entry.value),
    ];
  }

  Widget _defaultInstanceField({
    required String serviceType,
    required List<ServiceInstance> instances,
  }) {
    // Chaptarr has no global default, so an unset value means "no access";
    // every other service type falls back to its global default.
    final isChaptarr = serviceType == 'chaptarr';
    final value = _defaultInstances[serviceType];
    // Guard against a stored id that's no longer in the instance list.
    final hasValue = value != null && instances.any((i) => i.id == value);
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 4, 16, 12),
      child: DropdownButtonFormField<String?>(
        initialValue: hasValue ? value : null,
        isExpanded: true,
        dropdownColor: AppTheme.surfaceVariant,
        decoration: InputDecoration(
          labelText: 'Default ${_serviceLabel(serviceType)} instance',
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
          DropdownMenuItem<String?>(
            value: null,
            child: Text(
              isChaptarr ? 'No access' : 'Inherit (global default)',
              style: const TextStyle(color: AppTheme.textSecondary),
            ),
          ),
          ...instances.map(
            (i) => DropdownMenuItem<String?>(
              value: i.id,
              child: Text(i.name),
            ),
          ),
        ],
        onChanged: (v) => setState(() => _defaultInstances[serviceType] = v),
      ),
    );
  }

  String _serviceLabel(String serviceType) {
    switch (serviceType) {
      case 'radarr':
        return 'Radarr';
      case 'sonarr':
        return 'Sonarr';
      case 'chaptarr':
        return 'Chaptarr';
      case 'sabnzbd':
        return 'SABnzbd';
      case 'qbittorrent':
        return 'qBittorrent';
      case 'nzbget':
        return 'NZBGet';
      case 'transmission':
        return 'Transmission';
      case 'tautulli':
        return 'Tautulli';
      default:
        return serviceType;
    }
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
