import 'dart:async';

import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/layout/adaptive.dart';
import '../../../core/models/backend_connection.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/theme/app_theme.dart';
import '../data/content_policy_service.dart';
import '../data/request_settings_service.dart';
import '../../auth/logic/auth_provider.dart';
import '../../discover/data/discover_api_service.dart';
import '../../discover/data/tmdb_models.dart';
import '../../request/data/request_service.dart';

/// Admin screen for editing one user's per-user request overrides. A null
/// value for any option means "inherit the global default". It also leads
/// with the Kids account section: the switch and the limits the server
/// enforces on every title that account is shown.
class UserRequestSettingsScreen extends ConsumerStatefulWidget {
  const UserRequestSettingsScreen({
    super.key,
    required this.userId,
    required this.username,
    this.targetIsAdmin = false,
  });

  final int userId;
  final String username;

  /// An admin can never be a kids account, so the section is absent for one.
  final bool targetIsAdmin;

  @override
  ConsumerState<UserRequestSettingsScreen> createState() =>
      _UserRequestSettingsScreenState();
}

class _UserRequestSettingsScreenState
    extends ConsumerState<UserRequestSettingsScreen> {
  late final RequestSettingsService _service;
  late final ContentPolicyService _policyService;

  bool _isLoading = true;
  String? _error;
  bool _saving = false;

  // Kids account section. It loads beside the request settings with its own
  // failure states, so a ratings list that cannot be read never blocks the
  // rest of the screen. _kidsSupported is false on a server too old to
  // have kids accounts (the certifications route 404s), which hides the
  // section entirely; the policy read itself 404s for any non-child.
  bool _kidsLoading = true;
  bool _kidsSupported = false;
  bool _catalogFailed = false;
  bool _policyUnreadable = false;
  CertificationCatalog? _catalog;
  ContentPolicy? _loadedPolicy;
  bool _childEnabled = false;
  String _ratingRegion = 'US';
  String? _maxMovieRating;
  String? _maxTvRating;
  bool _blockUnrated = true;
  Set<int> _blockedMovieGenres = {};
  Set<int> _blockedTvGenres = {};
  List<Genre>? _movieGenres;
  List<Genre>? _tvGenres;
  Map<String, String> _regionNames = {};

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
  // (null = inherit the global default; for chaptarr, null = no per-user grant).
  Map<String, String?> _defaultInstances = {};

  // Additional instance access grants, keyed by service type. Grants widen
  // what the user may pick per request; they never move the default above.
  Map<String, Set<String>> _instanceGrants = {};

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      _service = RequestSettingsService(
        backendDio: ref.read(backendClientProvider),
      );
      _policyService = ContentPolicyService(
        backendDio: ref.read(backendClientProvider),
      );
      _load();
    });
  }

  String _friendlyError(Object e) {
    // Dio's toString omits the response body, so the server's own words
    // (a rating the region does not know, an admin that cannot be a kids
    // account) are read from the response first.
    if (e is DioException) {
      final data = e.response?.data;
      if (data is Map && data['error'] is String) return data['error'] as String;
    }
    final m = RegExp(r'"error":"([^"]+)"').firstMatch(e.toString());
    return m != null ? m.group(1)! : 'Something went wrong';
  }

  Future<void> _load() async {
    setState(() {
      _isLoading = true;
      _error = null;
    });
    unawaited(_loadKidsSection());
    try {
      final user = await _service.getUserSettings(widget.userId);
      final admin = await _service.getAdminSettings();
      final defaults = await _service.getUserDefaultInstances(widget.userId);
      final grants = await _service.getUserInstanceGrants(widget.userId);
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
        _instanceGrants = {
          for (final entry in grants.entries) entry.key: Set.of(entry.value),
        };
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

  /// Loads the Kids account section: the rating schemes (which double as
  /// the "does this server have kids accounts" probe), the user's policy,
  /// the genre lists, and the region names. Each part fails on its own.
  Future<void> _loadKidsSection() async {
    if (widget.targetIsAdmin) {
      if (mounted) setState(() => _kidsLoading = false);
      return;
    }
    setState(() {
      _kidsLoading = true;
      _catalogFailed = false;
      _policyUnreadable = false;
    });
    CertificationCatalog? catalog;
    var catalogFailed = false;
    try {
      catalog = await _policyService.certifications();
    } catch (_) {
      catalogFailed = true;
    }
    if (!mounted) return;
    if (catalog == null && !catalogFailed) {
      setState(() {
        _kidsSupported = false;
        _kidsLoading = false;
      });
      return;
    }
    ContentPolicy? policy;
    var unreadable = false;
    try {
      policy = await _policyService.getUserPolicy(widget.userId);
    } catch (_) {
      unreadable = true;
    }
    final discover = ref.read(discoverServiceProvider);
    final movieGenres = await _swallow(discover.movieGenres());
    final tvGenres = await _swallow(discover.tvGenres());
    final regions = await _swallow(discover.watchRegions());
    if (!mounted) return;
    setState(() {
      _kidsSupported = true;
      _catalog = catalog;
      _catalogFailed = catalogFailed;
      _policyUnreadable = unreadable;
      _loadedPolicy = policy;
      _childEnabled = policy != null;
      if (policy != null) {
        _ratingRegion = policy.ratingRegion;
        _maxMovieRating = policy.maxMovieRating;
        _maxTvRating = policy.maxTvRating;
        _blockUnrated = policy.blockUnrated;
        _blockedMovieGenres = Set.of(policy.blockedMovieGenres);
        _blockedTvGenres = Set.of(policy.blockedTvGenres);
      }
      _movieGenres = movieGenres;
      _tvGenres = tvGenres;
      _regionNames = {
        for (final region in regions ?? const <WatchRegion>[])
          region.code: region.name,
      };
      _kidsLoading = false;
    });
  }

  /// A failed side fetch (a genre list, the region names) leaves that part
  /// unknown rather than failing the section.
  Future<T?> _swallow<T>(Future<T> future) async {
    try {
      return await future;
    } catch (_) {
      return null;
    }
  }

  /// The policy as edited, or null when the switch is off.
  ContentPolicy? get _kidsDraft {
    if (!_childEnabled) return null;
    return ContentPolicy(
      maxMovieRating: _maxMovieRating ?? '',
      maxTvRating: _maxTvRating ?? '',
      ratingRegion: _ratingRegion,
      blockUnrated: _blockUnrated,
      blockedMovieGenres: _blockedMovieGenres.toList()..sort(),
      blockedTvGenres: _blockedTvGenres.toList()..sort(),
    );
  }

  /// Only a changed section is written: the policy PUT refuses an admin and
  /// a rating the region does not know, and an untouched section must not
  /// trip either.
  bool get _kidsDirty =>
      _kidsSupported && !widget.targetIsAdmin && _kidsDraft != _loadedPolicy;

  /// Both caps must be entries of the chosen region's schemes. While the
  /// schemes could not be read the stored caps are kept as they are.
  bool get _kidsValid {
    final catalog = _catalog;
    if (!_childEnabled || catalog == null || _catalogFailed) return true;
    return catalog
            .movieFor(_ratingRegion)
            .any((o) => o.certification == _maxMovieRating) &&
        catalog.tvFor(_ratingRegion).any((o) => o.certification == _maxTvRating);
  }

  void _onChildToggled(bool on) {
    setState(() {
      _childEnabled = on;
      if (!on) return;
      // A kids account's requests default to needing approval; the control
      // stays visible below so the admin can still decide otherwise.
      _requireApproval = true;
      if (_maxMovieRating == null || _maxTvRating == null) {
        _applyRegionDefaults(_ratingRegion);
      }
    });
  }

  void _applyRegionDefaults(String region) {
    final catalog = _catalog;
    if (catalog == null) return;
    _maxMovieRating =
        CertificationCatalog.defaultFor(catalog.movieFor(region))?.certification;
    _maxTvRating =
        CertificationCatalog.defaultFor(catalog.tvFor(region))?.certification;
  }

  Future<void> _save() async {
    if (_saving) return;
    setState(() => _saving = true);
    try {
      // The kids account write goes first, and only when it changed: it is
      // the one with refusals of its own, and refusing before anything else
      // is written leaves a clean "nothing saved" state.
      if (_kidsDirty) {
        final draft = _kidsDraft;
        if (draft != null) {
          await _policyService.updateUserPolicy(widget.userId, draft);
        } else {
          await _policyService.deleteUserPolicy(widget.userId);
        }
        _loadedPolicy = draft;
      }
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
      // Same rule for grants: name every visible type so an emptied set
      // clears its rows instead of being silently skipped.
      final grants = <String, List<String>>{
        for (final type in _instancesByType().keys)
          type: (_instanceGrants[type] ?? const <String>{}).toList()..sort(),
      };
      await _service.updateUserInstanceGrants(widget.userId, grants);
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
    // Subscribe to the auth state: the instance sections are derived from the
    // connection's instance list, and a read alone would freeze this screen
    // on whatever had loaded at first build.
    ref.watch(authProvider);
    return Scaffold(
      appBar: AppBar(title: Text('User Settings — ${widget.username}')),
      body: CenteredContent(
          child: _isLoading
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
                  : _buildBody()),
    );
  }

  Widget _buildBody() {
    final global = _global!;
    return ListView(
      padding: const EdgeInsets.symmetric(vertical: 8),
      children: [
        ..._buildKidsSection(),
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
                foregroundColor: AppTheme.onAccent,
                padding: const EdgeInsets.symmetric(vertical: 14),
              ),
              onPressed: _saving || !_kidsValid ? null : _save,
              child: _saving
                  ? const SizedBox(
                      width: 20,
                      height: 20,
                      child: CircularProgressIndicator(
                          strokeWidth: 2, color: AppTheme.onAccent),
                    )
                  : const Text('Save'),
            ),
          ),
        ),
        const SizedBox(height: 32),
      ],
    );
  }

  /// The Kids account section: the switch, then the limits while it is on.
  /// Absent for admins and on servers without kids accounts.
  List<Widget> _buildKidsSection() {
    if (widget.targetIsAdmin || !_kidsSupported) return const [];
    final catalog = _catalog;
    return [
      const Padding(
        padding: EdgeInsets.fromLTRB(16, 8, 16, 4),
        child: Text(
          'Kids account',
          style: TextStyle(
            color: AppTheme.textPrimary,
            fontWeight: FontWeight.w600,
          ),
        ),
      ),
      if (_policyUnreadable)
        _kidsNotice(
          "Couldn't read this user's kids account settings.",
          onRetry: _loadKidsSection,
        )
      else ...[
        SwitchListTile(
          value: _childEnabled,
          onChanged: _kidsLoading || (catalog == null && !_childEnabled)
              ? null
              : _onChildToggled,
          title: const Text(
            'Kids account',
            style: TextStyle(
                color: AppTheme.textPrimary, fontWeight: FontWeight.w500),
          ),
          subtitle: const Text(
            'Only shows this user titles within the limits below. The server '
            'filters every row, search, and page for this account.',
            style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
          ),
        ),
        if (_childEnabled) ...[
          if (_catalogFailed)
            _kidsNotice(
              "Couldn't load the ratings list. The stored ratings are kept.",
              onRetry: _loadKidsSection,
            ),
          _regionField(),
          _ratingField(
            key: ValueKey('movie-rating-$_ratingRegion'),
            label: 'Movies up to',
            options: catalog?.movieFor(_ratingRegion) ?? const [],
            value: _maxMovieRating,
            onChanged: (v) => setState(() => _maxMovieRating = v),
          ),
          _ratingField(
            key: ValueKey('tv-rating-$_ratingRegion'),
            label: 'Shows up to',
            options: catalog?.tvFor(_ratingRegion) ?? const [],
            value: _maxTvRating,
            onChanged: (v) => setState(() => _maxTvRating = v),
          ),
          SwitchListTile(
            value: _blockUnrated,
            onChanged: (v) => setState(() => _blockUnrated = v),
            title: const Text(
              'Hide unrated titles',
              style: TextStyle(
                  color: AppTheme.textPrimary, fontWeight: FontWeight.w500),
            ),
            subtitle: const Text(
              'A title with no rating in this region stays hidden.',
              style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
            ),
          ),
          const Padding(
            padding: EdgeInsets.fromLTRB(16, 8, 16, 4),
            child: Text(
              'Hidden genres',
              style: TextStyle(
                color: AppTheme.textPrimary,
                fontWeight: FontWeight.w500,
              ),
            ),
          ),
          _genreChips(
            label: 'Movies',
            genres: _movieGenres,
            hidden: _blockedMovieGenres,
            onToggle: (id, on) => setState(() {
              on ? _blockedMovieGenres.add(id) : _blockedMovieGenres.remove(id);
            }),
          ),
          _genreChips(
            label: 'Shows',
            genres: _tvGenres,
            hidden: _blockedTvGenres,
            onToggle: (id, on) => setState(() {
              on ? _blockedTvGenres.add(id) : _blockedTvGenres.remove(id);
            }),
          ),
        ],
      ],
      const Divider(color: AppTheme.border),
    ];
  }

  Widget _kidsNotice(String message, {required VoidCallback onRetry}) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 4, 16, 4),
      child: Row(
        children: [
          Expanded(
            child: Text(message,
                style: const TextStyle(color: AppTheme.warning, fontSize: 13)),
          ),
          TextButton(onPressed: onRetry, child: const Text('Retry')),
        ],
      ),
    );
  }

  Widget _regionField() {
    final codes = <String>{...?_catalog?.regions, _ratingRegion}.toList();
    String labelFor(String code) => _regionNames[code] ?? code;
    codes.sort((a, b) => labelFor(a).compareTo(labelFor(b)));
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 4, 16, 12),
      child: DropdownButtonFormField<String>(
        key: ValueKey('rating-region-${codes.length}'),
        initialValue: _ratingRegion,
        isExpanded: true,
        dropdownColor: AppTheme.surfaceVariant,
        decoration: const InputDecoration(
          labelText: 'Ratings region',
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
          for (final code in codes)
            DropdownMenuItem<String>(value: code, child: Text(labelFor(code))),
        ],
        onChanged: _catalog == null
            ? null
            : (v) {
                if (v == null) return;
                setState(() {
                  _ratingRegion = v;
                  _applyRegionDefaults(v);
                });
              },
      ),
    );
  }

  Widget _ratingField({
    required Key key,
    required String label,
    required List<CertificationOption> options,
    required String? value,
    required ValueChanged<String?> onChanged,
  }) {
    final known = options.any((o) => o.certification == value);
    final selected =
        known ? options.firstWhere((o) => o.certification == value) : null;
    // With no schemes to offer, the stored cap is shown as the one choice
    // and left alone.
    final items = _catalogFailed && value != null && !known
        ? [DropdownMenuItem<String>(value: value, child: Text(value))]
        : [
            for (final o in options)
              DropdownMenuItem<String>(
                  value: o.certification, child: Text(o.certification)),
          ];
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 4, 16, 12),
      child: DropdownButtonFormField<String>(
        key: key,
        initialValue: known || (_catalogFailed && value != null) ? value : null,
        isExpanded: true,
        dropdownColor: AppTheme.surfaceVariant,
        decoration: InputDecoration(
          labelText: label,
          labelStyle: const TextStyle(color: AppTheme.textSecondary),
          helperText: selected != null && selected.meaning.isNotEmpty
              ? selected.meaning
              : null,
          helperMaxLines: 3,
          errorText: !known && !_catalogFailed && options.isNotEmpty
              ? 'Choose a rating for this region'
              : null,
          enabledBorder: const OutlineInputBorder(
            borderSide: BorderSide(color: AppTheme.border),
          ),
          focusedBorder: const OutlineInputBorder(
            borderSide: BorderSide(color: AppTheme.accent),
          ),
        ),
        style: const TextStyle(color: AppTheme.textPrimary),
        items: items,
        onChanged: _catalogFailed ? null : onChanged,
      ),
    );
  }

  /// Hidden-genre chips for one media type. Selected means hidden. A stored
  /// id the list no longer names is kept as it is, never pruned.
  Widget _genreChips({
    required String label,
    required List<Genre>? genres,
    required Set<int> hidden,
    required void Function(int id, bool on) onToggle,
  }) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 4, 16, 8),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(label,
              style: const TextStyle(
                  color: AppTheme.textSecondary,
                  fontSize: 13,
                  fontWeight: FontWeight.w600)),
          const SizedBox(height: 6),
          if (genres == null)
            _kidsNotice("Couldn't load the genre list.",
                onRetry: _loadKidsSection)
          else
            Wrap(
              spacing: 8,
              runSpacing: 8,
              children: [
                for (final genre in genres)
                  FilterChip(
                    label: Text(genre.name),
                    selected: hidden.contains(genre.id),
                    onSelected: (on) => onToggle(genre.id, on),
                    showCheckmark: false,
                    selectedColor: AppTheme.accent.withValues(alpha: 0.2),
                    backgroundColor: AppTheme.surfaceVariant,
                    labelStyle: TextStyle(
                      color: hidden.contains(genre.id)
                          ? AppTheme.accent
                          : AppTheme.textPrimary,
                    ),
                    shape: RoundedRectangleBorder(
                      borderRadius: BorderRadius.circular(20),
                      side: BorderSide(
                          color: hidden.contains(genre.id)
                              ? AppTheme.accent
                              : AppTheme.border),
                    ),
                  ),
              ],
            ),
        ],
      ),
    );
  }

  /// The admin's connection lists every configured instance; group them by
  /// service type (first-seen order) so we can render one dropdown per type.
  /// Service types whose default instance is a per-user "source" override.
  /// Download clients, Tautulli and Tracearr are admin-only infrastructure
  /// (not a per-user content source), so they are excluded from this section.
  static const _sourceServiceTypes = {'radarr', 'sonarr', 'chaptarr', 'lidarr'};

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
          'Pin which instance this user defaults to per service. For regular '
          'users, choosing a Chaptarr instance grants Books access and a '
          'Lidarr instance grants Music access. Admins can pin their own '
          'request target here too. Below each default, extra libraries can '
          'be granted so the user chooses per request — a grant never moves '
          'the default.',
          style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
        ),
      ),
      for (final entry in grouped.entries) ...[
        _defaultInstanceField(serviceType: entry.key, instances: entry.value),
        if (entry.value.length > 1)
          _instanceGrantsField(serviceType: entry.key, instances: entry.value),
      ],
    ];
  }

  /// Checkbox list of additional library grants for one service type, shown
  /// only when siblings exist (a single-instance type has nothing extra to
  /// grant).
  Widget _instanceGrantsField({
    required String serviceType,
    required List<ServiceInstance> instances,
  }) {
    final granted = _instanceGrants[serviceType] ?? const <String>{};
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 0, 16, 12),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            'Also grant ${_serviceLabel(serviceType)} libraries',
            style: const TextStyle(
              color: AppTheme.textSecondary,
              fontSize: 13,
              fontWeight: FontWeight.w600,
            ),
          ),
          for (final inst in instances)
            CheckboxListTile(
              dense: true,
              contentPadding: EdgeInsets.zero,
              controlAffinity: ListTileControlAffinity.leading,
              activeColor: AppTheme.accent,
              title: Text(inst.name,
                  style: const TextStyle(color: AppTheme.textPrimary)),
              value: granted.contains(inst.id),
              onChanged: (checked) => setState(() {
                final next = Set<String>.of(granted);
                if (checked == true) {
                  next.add(inst.id);
                } else {
                  next.remove(inst.id);
                }
                _instanceGrants[serviceType] = next;
              }),
            ),
        ],
      ),
    );
  }

  Widget _defaultInstanceField({
    required String serviceType,
    required List<ServiceInstance> instances,
  }) {
    // Chaptarr and Lidarr grants are per-user for regular users. Admins can
    // also use this setting to pin their own request target.
    final isGrantOnly = serviceType == 'chaptarr' || serviceType == 'lidarr';
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
              isGrantOnly
                  ? 'No per-user ${_serviceLabel(serviceType)} assignment'
                  : 'Inherit (global default)',
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
      case 'lidarr':
        return 'Lidarr';
      case 'sabnzbd':
        return 'SABnzbd';
      case 'qbittorrent':
        return 'qBittorrent';
      case 'nzbget':
        return 'NZBGet';
      case 'transmission':
        return 'Transmission';
      case 'deluge':
        return 'Deluge';
      case 'tautulli':
        return 'Tautulli';
      case 'tracearr':
        return 'Tracearr';
      case 'jellyfin':
        return 'Jellyfin';
      case 'emby':
        return 'Emby';
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
        color: selected ? AppTheme.onAccent : AppTheme.textSecondary,
        fontWeight: selected ? FontWeight.w600 : FontWeight.w400,
      ),
    );
  }
}
