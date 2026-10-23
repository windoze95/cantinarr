import '../../discover/ui/browse_labels.dart';
import 'package:cantinarr/l10n/l10n.dart';
import '../../request/ui/request_allowance_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/layout/adaptive.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/settings_highlight.dart';
import '../../../core/widgets/unsaved_changes_guard.dart';
import '../data/request_settings_service.dart';
import '../settings_anchors.dart';
import '../../request/data/request_service.dart';
import '../../request/data/tv_match_service.dart';
import 'package:go_router/go_router.dart';

/// Admin screen for editing the global media-request defaults.
class RequestSettingsScreen extends ConsumerStatefulWidget {
  /// Settings-search anchor to scroll to and flash on arrival.
  final String? highlightId;

  const RequestSettingsScreen({super.key, this.highlightId});

  @override
  ConsumerState<RequestSettingsScreen> createState() =>
      _RequestSettingsScreenState();
}

class _RequestSettingsScreenState extends ConsumerState<RequestSettingsScreen> {
  late final RequestSettingsService _service;

  final _draft = SettingsDraft();
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
    return m != null ? m.group(1)! : context.l10n.notificationSomethingWrong;
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
        _draft.markSaved(_edited!.toJson());
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
        _draft.markSaved(_edited!.toJson());
        _saving = false;
      });
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(context.l10n.settingsSaved)),
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
  Widget build(BuildContext context) => UnsavedChangesGuard(
        hasChanges: () =>
            _edited != null &&
            _admin != null &&
            _draft.hasChanges(_edited!.toJson()),
        isSaving: _saving,
        child: _buildPage(context),
      );

  Widget _buildPage(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: Text(context.l10n.settingsRequestDefaults)),
      body: CenteredContent(
          child: _isLoading
              ? const Center(
                  child: CircularProgressIndicator(color: AppTheme.accent))
              : (_admin == null || _edited == null)
                  ? Center(
                      child: Padding(
                        padding: const EdgeInsets.all(24),
                        child: Column(
                          mainAxisSize: MainAxisSize.min,
                          children: [
                            Text(
                                _error ??
                                    context.l10n.notificationSomethingWrong,
                                style: const TextStyle(color: AppTheme.error),
                                textAlign: TextAlign.center),
                            const SizedBox(height: 12),
                            ElevatedButton(
                                onPressed: _load,
                                child: Text(context.l10n.settingsRetry)),
                          ],
                        ),
                      ),
                    )
                  : _buildBody(_admin!, _edited!)),
    );
  }

  Widget _buildBody(AdminRequestSettings admin, GlobalRequestSettings edited) {
    return ListView(
      // Build every child while a settings-search highlight needs to find
      // its anchor (see SettingsHighlight).
      cacheExtent: SettingsHighlight.cacheExtentFor(widget.highlightId),
      padding: const EdgeInsets.symmetric(vertical: 8),
      children: [
        Padding(
          padding: EdgeInsets.fromLTRB(16, 8, 16, 4),
          child: Text(
            context.l10n.settingsTheseDefaultsApplyToEveryUserUnlessOverridden,
            style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
          ),
        ),
        const Padding(
            padding: EdgeInsets.all(16),
            child: RequestAllowanceSection(editDefaults: true)),
        _SectionLabel(context.l10n.settingsApproval),
        SettingsHighlight(
          anchorId: SettingsAnchors.requestsRequireApproval,
          highlightId: widget.highlightId,
          child: SwitchListTile(
            value: edited.requireApproval,
            onChanged: (v) =>
                setState(() => _edited = edited.copyWith(requireApproval: v)),
            title: Text(
              context.l10n.settingsRequireApprovalForNewRequests,
              style: TextStyle(
                  color: AppTheme.textPrimary, fontWeight: FontWeight.w500),
            ),
            subtitle: Text(
              context.l10n.settingsNewRequestsWaitInAQueueForAn,
              style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
            ),
          ),
        ),
        _SectionLabel(context.l10n.settingsSeasons),
        if (ref.watch(tvMatchesAllowedProvider))
          ListTile(
            leading: const Icon(Icons.rule),
            title: Text(context.l10n.settingsTvMatches),
            subtitle: Text(context
                .l10n.settingsCorrectSeriesAndSeasonMatchingAndReviewAffected),
            trailing: const Icon(Icons.chevron_right),
            onTap: () => context.push('/settings/tv-matches'),
          ),
        SettingsHighlight(
          anchorId: SettingsAnchors.requestsSeasonChoice,
          highlightId: widget.highlightId,
          child: SwitchListTile(
            value: edited.allowSeasonChoice,
            onChanged: (v) =>
                setState(() => _edited = edited.copyWith(allowSeasonChoice: v)),
            title: Text(
              context.l10n.settingsLetUsersChooseSeasons,
              style: TextStyle(
                  color: AppTheme.textPrimary, fontWeight: FontWeight.w500),
            ),
            subtitle: Text(
              context.l10n.settingsAllowUsersToPickWhichSeasonsToRequest,
              style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
            ),
          ),
        ),
        SettingsHighlight(
          anchorId: SettingsAnchors.requestsSeasonScope,
          highlightId: widget.highlightId,
          child: ListTile(
            title: Text(
              context.l10n.settingsDefaultSeasonScope,
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
                    child: Text(browseLabel(context, c.label)),
                  ),
              ],
              onChanged: (v) {
                if (v == null) return;
                setState(
                    () => _edited = edited.copyWith(defaultSeasonScope: v));
              },
            ),
          ),
        ),
        _SectionLabel(context.l10n.settingsQuality),
        SettingsHighlight(
          anchorId: SettingsAnchors.requestsQualityChoice,
          highlightId: widget.highlightId,
          child: SwitchListTile(
            value: edited.allowQualityChoice,
            onChanged: (v) => setState(
                () => _edited = edited.copyWith(allowQualityChoice: v)),
            title: Text(
              context.l10n.settingsLetUsersChooseQuality,
              style: TextStyle(
                  color: AppTheme.textPrimary, fontWeight: FontWeight.w500),
            ),
            subtitle: Text(
              context.l10n.settingsOffByDefaultWhenOffUsersGetThe,
              style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
            ),
          ),
        ),
        SettingsHighlight(
          anchorId: SettingsAnchors.requestsQualityRadarr,
          highlightId: widget.highlightId,
          child: _qualityTile(
            label: context.l10n.settingsDefaultRadarrQuality,
            value: edited.defaultQualityRadarr,
            profiles: admin.radarrProfiles,
            onChanged: (v) => setState(
                () => _edited = edited.copyWith(defaultQualityRadarr: v)),
          ),
        ),
        SettingsHighlight(
          anchorId: SettingsAnchors.requestsQualitySonarr,
          highlightId: widget.highlightId,
          child: _qualityTile(
            label: context.l10n.settingsDefaultSonarrQuality,
            value: edited.defaultQualitySonarr,
            profiles: admin.sonarrProfiles,
            onChanged: (v) => setState(
                () => _edited = edited.copyWith(defaultQualitySonarr: v)),
          ),
        ),
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 24, 16, 16),
          child: SizedBox(
            width: double.infinity,
            child: ElevatedButton(
              style: ElevatedButton.styleFrom(
                backgroundColor: AppTheme.accent,
                foregroundColor: AppTheme.onAccent,
                padding: const EdgeInsets.symmetric(vertical: 14),
              ),
              onPressed: _saving ? null : _save,
              child: _saving
                  ? const SizedBox(
                      width: 20,
                      height: 20,
                      child: CircularProgressIndicator(
                        color: AppTheme.onAccent,
                        strokeWidth: 2,
                      ),
                    )
                  : Text(context.l10n.settingsSave),
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
    final hasValue = value == 0 || profiles.any((p) => p.id == value);
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
          DropdownMenuItem<int>(
            value: 0,
            child: Text(context.l10n.settingsServerDefault),
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
