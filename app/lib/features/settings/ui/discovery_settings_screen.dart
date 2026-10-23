import '../../discover/ui/browse_labels.dart';
import 'settings_labels.dart';
import 'package:cantinarr/l10n/l10n.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/layout/adaptive.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/settings_highlight.dart';
import '../../../core/widgets/status_pill.dart';
import '../../../core/widgets/unsaved_changes_guard.dart';
import '../../auth/logic/auth_provider.dart';
import '../../discover/logic/discovery_access.dart';
import '../data/credentials_service.dart';
import '../data/discovery_settings_service.dart';
import '../settings_anchors.dart';
import 'credential_section.dart';

/// Admin screen for the Discover module: which feed backs the headline rows,
/// whether those rows drop non-English titles, and the TMDB/Trakt credentials
/// those feeds run on.
class DiscoverySettingsScreen extends ConsumerStatefulWidget {
  /// Settings-search anchor to scroll to and flash on arrival.
  final String? highlightId;

  const DiscoverySettingsScreen({super.key, this.highlightId});

  @override
  ConsumerState<DiscoverySettingsScreen> createState() =>
      _DiscoverySettingsScreenState();
}

class _DiscoverySettingsScreenState
    extends ConsumerState<DiscoverySettingsScreen> {
  late final DiscoverySettingsService _service;
  late final CredentialsService _credentialsService;

  final _draft = SettingsDraft();
  Object get _draftValues => [
        _edited?.source,
        _edited?.englishOnly,
        _edited?.hiddenWhenUnconfigured,
        _edited?.cover4KBadges,
        _tmdbController.text,
        _traktIdController.text,
      ];
  DiscoverySettings? _edited;
  CredentialsStatus? _credentials;
  bool _isLoading = true;
  String? _error;
  bool _saving = false;

  final _tmdbController = TextEditingController();
  final _traktIdController = TextEditingController();

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      _service = DiscoverySettingsService(
        backendDio: ref.read(backendClientProvider),
      );
      _credentialsService = CredentialsService(
        backendDio: ref.read(backendClientProvider),
      );
      _load();
    });
  }

  @override
  void dispose() {
    _tmdbController.dispose();
    _traktIdController.dispose();
    super.dispose();
  }

  String _friendlyError(Object e) {
    final m = RegExp(r'"error":"([^"]+)"').firstMatch(e.toString());
    return m != null ? m.group(1)! : context.l10n.notificationSomethingWrong;
  }

  Future<void> _load() async {
    setState(() {
      _isLoading = _edited == null;
      _error = null;
    });
    try {
      final results = await Future.wait<Object>([
        _service.get(),
        _credentialsService.getStatus(),
      ]);
      if (!mounted) return;
      setState(() {
        _edited = results[0] as DiscoverySettings;
        _credentials = results[1] as CredentialsStatus;
        _draft.markSaved(_draftValues);
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
    final edited = _edited;
    if (edited == null || _saving) return;
    setState(() => _saving = true);
    try {
      // Credentials first: a new Trakt client ID can make a source
      // selectable, and the reload below lets the rows reflect it.
      final creds = <String, String>{};
      if (_tmdbController.text.isNotEmpty) {
        creds['tmdb_access_token'] = _tmdbController.text.trim();
      }
      if (_traktIdController.text.isNotEmpty) {
        creds['trakt_client_id'] = _traktIdController.text.trim();
      }
      if (creds.isNotEmpty) {
        await _credentialsService.update(creds);
        _tmdbController.clear();
        _traktIdController.clear();
        // Capability flags (services.tmdb/trakt) live in /api/config.
        await ref.read(authProvider.notifier).refreshConfig();
      }
      final saved = await _service.update(edited);
      if (!mounted) return;
      _edited = saved;
      _draft.markSaved(_draftValues);
      final refreshed = creds.isEmpty ? null : await _reloadAfterCreds();
      if (!mounted) return;
      setState(() {
        _edited = refreshed ?? saved;
        _draft.markSaved(_draftValues);
        _saving = false;
      });
      try {
        await ref.read(authProvider.notifier).refreshConfig();
      } catch (_) {}
      if (!mounted) return;
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

  /// Re-reads settings + credential status after a credential change, so the
  /// source list's selectability and the status chips are live truth.
  Future<DiscoverySettings?> _reloadAfterCreds() async {
    final results = await Future.wait<Object>([
      _service.get(),
      _credentialsService.getStatus(),
    ]);
    if (!mounted) return null;
    _credentials = results[1] as CredentialsStatus;
    return results[0] as DiscoverySettings;
  }

  Future<void> _deleteCredential(String key, String label,
      {String? message}) async {
    final confirm = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title:
            Text(context.l10n.settingsMessageRemoveValue1((label).toString())),
        content: Text(message ??
            context.l10n.settingsMessageThisWillDisableTheValue1Integration(
                (label).toString())),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx, false),
            child: Text(context.l10n.settingsCancel),
          ),
          TextButton(
            onPressed: () => Navigator.pop(ctx, true),
            child: Text(context.l10n.settingsRemove,
                style: TextStyle(color: AppTheme.error)),
          ),
        ],
      ),
    );
    if (confirm != true) return;

    try {
      await _credentialsService.delete(key);
      // Losing Trakt can invalidate the selected source server-side; reload
      // both settings and capability flags rather than trusting local state.
      await ref.read(authProvider.notifier).refreshConfig();
      final refreshed = await _reloadAfterCreds();
      if (!mounted) return;
      setState(() {
        if (refreshed != null) _edited = refreshed;
      });
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
            content: Text(context.l10n
                .settingsMessageValue1CredentialRemoved((label).toString()))),
      );
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
            content: Text(context.l10n
                .settingsMessageFailedToRemoveValue1((e).toString()))),
      );
    }
  }

  @override
  Widget build(BuildContext context) => UnsavedChangesGuard(
        hasChanges: () => _draft.hasChanges(_draftValues),
        isSaving: _saving,
        child: _buildPage(context),
      );

  Widget _buildPage(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: Text(context.l10n.settingsDiscover)),
      body: CenteredContent(
        child: _isLoading
            ? const Center(
                child: CircularProgressIndicator(color: AppTheme.accent))
            : _edited == null
                ? Center(
                    child: Padding(
                      padding: const EdgeInsets.all(24),
                      child: Column(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          Text(
                              _error ?? context.l10n.notificationSomethingWrong,
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
                : _buildBody(_edited!),
      ),
    );
  }

  /// One row-source choice. Built from a ListTile rather than a RadioListTile
  /// so each option can carry its explanation, and so the screen stays clear of
  /// the deprecated radio-group API.
  Widget _sourceTile(DiscoverySettings edited, DiscoverySource source) {
    final selectable = edited.isSelectable(source);
    final selected = edited.source == source.value;
    return Semantics(
      inMutuallyExclusiveGroup: true,
      selected: selected,
      enabled: selectable,
      child: ListTile(
        onTap: selectable
            ? () => setState(
                  () => _edited = edited.copyWith(source: source.value),
                )
            : null,
        enabled: selectable,
        leading: Icon(
          selected ? Icons.radio_button_checked : Icons.radio_button_off,
          color: selectable ? AppTheme.accent : AppTheme.textSecondary,
        ),
        title: Row(
          children: [
            Flexible(
              child: Text(
                settingsModelLabel(context, source.label),
                style: TextStyle(
                  color: selectable
                      ? AppTheme.textPrimary
                      : AppTheme.textSecondary,
                  fontWeight: FontWeight.w500,
                ),
              ),
            ),
            if (source.recommended) ...[
              const SizedBox(width: 8),
              StatusPill(
                text: context.l10n.settingsRecommended,
                color: selectable ? AppTheme.accent : AppTheme.textSecondary,
              ),
            ],
          ],
        ),
        subtitle: Text(
          selectable
              ? settingsModelLabel(context, source.description)
              : context.l10n.settingsAddATraktClientIdBelowToUse,
          style: const TextStyle(color: AppTheme.textSecondary, fontSize: 13),
        ),
      ),
    );
  }

  Widget _buildBody(DiscoverySettings edited) {
    return ListView(
      // Build every child while a settings-search highlight needs to find
      // its anchor (see SettingsHighlight).
      cacheExtent: SettingsHighlight.cacheExtentFor(widget.highlightId),
      padding: const EdgeInsets.symmetric(vertical: 8),
      children: [
        Padding(
          padding: EdgeInsets.fromLTRB(16, 8, 16, 4),
          child: Text(
            context.l10n.settingsTheseSettingsShapeTheHeadlineRowOnThe,
            style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
          ),
        ),
        _SectionLabel(context.l10n.settingsRowSource),
        for (final source in edited.sources) _sourceTile(edited, source),
        _SectionLabel(context.l10n.settingsLanguage),
        SettingsHighlight(
          anchorId: SettingsAnchors.discoveryEnglishOnly,
          highlightId: widget.highlightId,
          child: SwitchListTile(
            value: edited.englishOnly,
            onChanged: (v) =>
                setState(() => _edited = edited.copyWith(englishOnly: v)),
            title: Text(
              context.l10n.settingsOnlyShowEnglishLanguageTitles,
              style: TextStyle(
                  color: AppTheme.textPrimary, fontWeight: FontWeight.w500),
            ),
            subtitle: Text(
              context.l10n.settingsHidesTitlesWhoseOriginalLanguageIsNotEnglish,
              style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
            ),
          ),
        ),
        _SectionLabel(context.l10n.settingsCovers),
        SettingsHighlight(
          anchorId: SettingsAnchors.discoveryCover4KBadges,
          highlightId: widget.highlightId,
          child: SwitchListTile(
            key: const Key('discovery-cover-4k-badges'),
            value: edited.cover4KBadges ?? false,
            // A server that predates the setting would silently drop it.
            onChanged: edited.cover4KBadges == null
                ? null
                : (v) =>
                    setState(() => _edited = edited.copyWith(cover4KBadges: v)),
            title: Text(
              context.l10n.settingsShow4KBadges,
              style: TextStyle(
                  color: AppTheme.textPrimary, fontWeight: FontWeight.w500),
            ),
            subtitle: Text(
              edited.cover4KBadges == null
                  ? context.l10n.settingsUpdateYourCantinarrServerToShow4KBadges
                  : context.l10n.settingsMarksMovieAndShowCoversWhenTheLibrary,
              style:
                  const TextStyle(color: AppTheme.textSecondary, fontSize: 13),
            ),
          ),
        ),
        _SectionLabel(context.l10n.settingsDiscoverTabs),
        Padding(
          padding: EdgeInsets.symmetric(horizontal: 16, vertical: 8),
          child: Text(context.l10n
              .settingsTheseChoicesHideUnconfiguredCatalogsDuringInitialSetup),
        ),
        if (edited.hiddenWhenUnconfigured == null)
          const Padding(
              padding: EdgeInsets.all(16),
              child: Text(discoverVisibilityUpdateMessage)),
        for (final tab in discoverCatalogs)
          SettingsHighlight(
            anchorId: SettingsAnchors.discoveryTabAnchors[tab.mediaType]!,
            highlightId: widget.highlightId,
            child: SwitchListTile(
              title: Text(context.l10n
                  .settingsMessageHideValue1WhenValue2IsnTConnected(
                      browseLabel(context, tab.label),
                      (tab.serviceName).toString())),
              value: edited.hiddenWhenUnconfigured?[tab.mediaType] ?? false,
              onChanged: edited.hiddenWhenUnconfigured == null || _saving
                  ? null
                  : (value) => setState(
                      () => _edited = edited.copyWith(hiddenWhenUnconfigured: {
                            ...edited.hiddenWhenUnconfigured!,
                            tab.mediaType: value,
                          })),
            ),
          ),
        _SectionLabel(context.l10n.settingsCredentials),
        // Trakt leads: it is the credential that unlocks a source, while
        // TMDB is an optional override of the built-in key.
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 8, 16, 0),
          child: SettingsHighlight(
            anchorId: SettingsAnchors.credentialsTrakt,
            highlightId: widget.highlightId,
            child: CredentialSection(
              title: 'Trakt',
              description:
                  context.l10n.settingsTrendingPopularListsAndTheCalendarRunOn,
              isConfigured:
                  _credentials?.isConfigured('trakt_client_id') ?? false,
              builtinActive: _credentials?.traktUsingBuiltin ?? false,
              controller: _traktIdController,
              hint: context.l10n.settingsTraktClientId,
              onDelete: () => _deleteCredential(
                'trakt_client_id',
                'Trakt',
                message:
                    context.l10n.settingsYourClientIdWillBeRemovedAndCantinarr,
              ),
            ),
          ),
        ),
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 20, 16, 0),
          child: SettingsHighlight(
            anchorId: SettingsAnchors.credentialsTmdb,
            highlightId: widget.highlightId,
            child: CredentialSection(
              title: 'TMDB',
              description:
                  context.l10n.settingsDiscoveryAndSearchRunOnCantinarrSBuilt,
              isConfigured:
                  _credentials?.isConfigured('tmdb_access_token') ?? false,
              builtinActive: _credentials?.tmdbUsingBuiltin ?? false,
              controller: _tmdbController,
              hint: context.l10n.settingsTmdbAccessToken,
              onDelete: () => _deleteCredential(
                'tmdb_access_token',
                'TMDB',
                message:
                    context.l10n.settingsYourTokenWillBeRemovedAndCantinarrS,
              ),
            ),
          ),
        ),
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 24, 16, 16),
          child: SizedBox(
            width: double.infinity,
            child: ElevatedButton(
              key: const Key('discovery-save'),
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
