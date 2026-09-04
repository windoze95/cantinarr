import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/layout/adaptive.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/error_banner.dart';
import '../data/radarr_api_service.dart';
import '../data/radarr_models.dart';

/// Edit a movie's Radarr settings: monitored, quality profile, minimum
/// availability, path and tags. Saving re-reads the movie and PUTs it back with
/// only these fields changed. Pops `true` after a successful update so callers
/// can reload. Admin only (the proxy requires instances:manage for the PUT).
/// The movie-side mirror of the series editor.
class EditMovieScreen extends ConsumerStatefulWidget {
  final String instanceId;
  final RadarrMovie movie;

  const EditMovieScreen({
    super.key,
    required this.instanceId,
    required this.movie,
  });

  @override
  ConsumerState<EditMovieScreen> createState() => _EditMovieScreenState();
}

class _EditMovieScreenState extends ConsumerState<EditMovieScreen> {
  /// Radarr's minimum-availability choices: when it starts searching.
  static const _availabilities = ['announced', 'inCinemas', 'released'];

  static String _availabilityLabel(String value) => switch (value) {
        'announced' => 'Announced',
        'inCinemas' => 'In Cinemas',
        'released' => 'Released',
        _ => value,
      };

  late final RadarrApiService _service;
  bool _isLoading = true;
  bool _isSaving = false;
  String? _error;

  List<RadarrQualityProfile> _profiles = [];
  // Null when the tag list couldn't be loaded — the Tags row is hidden then.
  List<RadarrTag>? _allTags;

  late bool _monitored;
  late int _qualityProfileId;
  late String _minimumAvailability;
  late String _path;
  late Set<int> _tagIds;

  @override
  void initState() {
    super.initState();
    _service = RadarrApiService(
      backendDio: ref.read(backendClientProvider),
      instanceId: widget.instanceId,
    );
    _seedFrom(widget.movie);
    WidgetsBinding.instance.addPostFrameCallback((_) => _load());
  }

  void _seedFrom(RadarrMovie m) {
    _monitored = m.monitored;
    _qualityProfileId = m.qualityProfileId;
    _minimumAvailability = m.minimumAvailability;
    _path = m.path ?? '';
    _tagIds = {...m.tags};
  }

  Future<void> _load() async {
    setState(() => _isLoading = true);
    try {
      // Profiles and the fresh movie are required; tags are optional.
      final profilesFuture = _service.getQualityProfiles();
      final movieFuture = _service.getMovieById(widget.movie.id);
      final profiles = await profilesFuture;
      final movie = await movieFuture;
      List<RadarrTag>? tags;
      try {
        tags = await _service.getTags();
      } catch (_) {
        tags = null;
      }
      if (!mounted) return;
      setState(() {
        _profiles = profiles;
        _allTags = tags;
        _seedFrom(movie);
        _isLoading = false;
        _error = null;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _isLoading = false;
        _error = 'Failed to load movie settings: $e';
      });
    }
  }

  Future<void> _save() async {
    setState(() => _isSaving = true);
    try {
      await _service.updateMovieFields(widget.movie.id, {
        'monitored': _monitored,
        'qualityProfileId': _qualityProfileId,
        'minimumAvailability': _minimumAvailability,
        'path': _path,
        'tags': _tagIds.toList()..sort(),
      });
      if (!mounted) return;
      ScaffoldMessenger.of(context)
          .showSnackBar(const SnackBar(content: Text('Movie updated')));
      Navigator.of(context).pop(true);
    } catch (e) {
      if (!mounted) return;
      setState(() => _isSaving = false);
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text('Update failed: $e')));
    }
  }

  String get _profileName {
    for (final p in _profiles) {
      if (p.id == _qualityProfileId) return p.name;
    }
    return 'Profile $_qualityProfileId';
  }

  String get _tagsSummary {
    final tags = _allTags;
    if (tags == null || _tagIds.isEmpty) return 'Not Set';
    final labels = [
      for (final t in tags)
        if (_tagIds.contains(t.id)) t.label,
    ];
    return labels.isEmpty ? 'Not Set' : labels.join(', ');
  }

  Future<void> _pickQualityProfile() async {
    final picked = await showDialog<int>(
      context: context,
      builder: (ctx) => SimpleDialog(
        backgroundColor: AppTheme.surface,
        title: const Text('Quality Profile'),
        children: _profiles
            .map((p) => SimpleDialogOption(
                  onPressed: () => Navigator.pop(ctx, p.id),
                  child: Text(p.name,
                      style: TextStyle(
                        color: p.id == _qualityProfileId
                            ? AppTheme.accent
                            : AppTheme.textPrimary,
                        fontSize: 15,
                      )),
                ))
            .toList(),
      ),
    );
    if (picked != null) setState(() => _qualityProfileId = picked);
  }

  Future<void> _pickMinimumAvailability() async {
    // A value Radarr no longer offers (an older install's) stays selectable
    // so opening the editor never silently changes it.
    final options = [
      ..._availabilities,
      if (!_availabilities.contains(_minimumAvailability)) _minimumAvailability,
    ];
    final picked = await showDialog<String>(
      context: context,
      builder: (ctx) => SimpleDialog(
        backgroundColor: AppTheme.surface,
        title: const Text('Minimum Availability'),
        children: options
            .map((a) => SimpleDialogOption(
                  onPressed: () => Navigator.pop(ctx, a),
                  child: Text(
                    _availabilityLabel(a),
                    style: TextStyle(
                      color: a == _minimumAvailability
                          ? AppTheme.accent
                          : AppTheme.textPrimary,
                      fontSize: 15,
                    ),
                  ),
                ))
            .toList(),
      ),
    );
    if (picked != null) setState(() => _minimumAvailability = picked);
  }

  Future<void> _editPath() async {
    final controller = TextEditingController(text: _path);
    final picked = await showDialog<String>(
      context: context,
      builder: (ctx) => AlertDialog(
        backgroundColor: AppTheme.surface,
        title: const Text('Movie Path'),
        content: TextField(
          controller: controller,
          autofocus: true,
          style: const TextStyle(color: AppTheme.textPrimary, fontSize: 14),
        ),
        actions: [
          TextButton(
              onPressed: () => Navigator.pop(ctx), child: const Text('Cancel')),
          TextButton(
            onPressed: () => Navigator.pop(ctx, controller.text.trim()),
            child: const Text('Set'),
          ),
        ],
      ),
    );
    controller.dispose();
    if (picked != null && picked.isNotEmpty) setState(() => _path = picked);
  }

  Future<void> _pickTags() async {
    final tags = _allTags;
    if (tags == null) return;
    final selection = {..._tagIds};
    final picked = await showDialog<Set<int>>(
      context: context,
      builder: (ctx) => StatefulBuilder(
        builder: (ctx, setDialogState) => AlertDialog(
          backgroundColor: AppTheme.surface,
          title: const Text('Tags'),
          content: SizedBox(
            width: double.maxFinite,
            child: tags.isEmpty
                ? const Text('No tags defined in Radarr.',
                    style:
                        TextStyle(color: AppTheme.textSecondary, fontSize: 14))
                : ListView(
                    shrinkWrap: true,
                    children: tags
                        .map((t) => CheckboxListTile(
                              value: selection.contains(t.id),
                              onChanged: (v) => setDialogState(() => v == true
                                  ? selection.add(t.id)
                                  : selection.remove(t.id)),
                              title: Text(t.label,
                                  style: const TextStyle(fontSize: 14)),
                              contentPadding: EdgeInsets.zero,
                              controlAffinity: ListTileControlAffinity.leading,
                              activeColor: AppTheme.accent,
                            ))
                        .toList(),
                  ),
          ),
          actions: [
            TextButton(
                onPressed: () => Navigator.pop(ctx),
                child: const Text('Cancel')),
            TextButton(
              onPressed: () => Navigator.pop(ctx, selection),
              child: const Text('Done'),
            ),
          ],
        ),
      ),
    );
    if (picked != null) setState(() => _tagIds = picked);
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: AppTheme.background,
      appBar: AppBar(
        backgroundColor: AppTheme.background,
        title: const Text('Edit Movie'),
      ),
      body: CenteredContent(child: _buildBody()),
    );
  }

  Widget _buildBody() {
    if (_isLoading) {
      return const Center(
          child: CircularProgressIndicator(color: AppTheme.accent));
    }
    if (_error != null) {
      return FullScreenError(message: _error!, onRetry: _load);
    }
    return Column(
      children: [
        Expanded(
          child: ListView(
            padding: const EdgeInsets.symmetric(vertical: 12),
            children: [
              _SettingCard(
                child: SwitchListTile(
                  value: _monitored,
                  onChanged: (v) => setState(() => _monitored = v),
                  title: const Text('Monitored',
                      style:
                          TextStyle(color: AppTheme.textPrimary, fontSize: 15)),
                  activeTrackColor: AppTheme.accent,
                ),
              ),
              _SettingCard(
                child: _PickerTile(
                  title: 'Quality Profile',
                  value: _profileName,
                  onTap: _pickQualityProfile,
                ),
              ),
              _SettingCard(
                child: _PickerTile(
                  title: 'Minimum Availability',
                  value: _availabilityLabel(_minimumAvailability),
                  onTap: _pickMinimumAvailability,
                ),
              ),
              _SettingCard(
                child: _PickerTile(
                  title: 'Movie Path',
                  value: _path.isEmpty ? 'Not Set' : _path,
                  onTap: _editPath,
                ),
              ),
              if (_allTags != null)
                _SettingCard(
                  child: _PickerTile(
                    title: 'Tags',
                    value: _tagsSummary,
                    onTap: _pickTags,
                  ),
                ),
            ],
          ),
        ),
        Container(
          padding: EdgeInsets.fromLTRB(
              12, 10, 12, 10 + MediaQuery.of(context).padding.bottom),
          decoration: const BoxDecoration(
            color: AppTheme.surface,
            border: Border(top: BorderSide(color: AppTheme.border, width: 0.5)),
          ),
          child: SizedBox(
            width: double.infinity,
            child: OutlinedButton.icon(
              onPressed: _isSaving ? null : _save,
              icon: _isSaving
                  ? const SizedBox(
                      width: 16,
                      height: 16,
                      child: CircularProgressIndicator(
                          strokeWidth: 2, color: AppTheme.accent))
                  : const Icon(Icons.edit_outlined,
                      size: 18, color: AppTheme.accent),
              label: const Text('Update',
                  style: TextStyle(color: AppTheme.textPrimary)),
              style: OutlinedButton.styleFrom(
                side: const BorderSide(color: AppTheme.border),
                shape: RoundedRectangleBorder(
                    borderRadius: BorderRadius.circular(10)),
                padding: const EdgeInsets.symmetric(vertical: 12),
              ),
            ),
          ),
        ),
      ],
    );
  }
}

class _SettingCard extends StatelessWidget {
  final Widget child;
  const _SettingCard({required this.child});

  @override
  Widget build(BuildContext context) {
    // A Material (not a decorated Container) so the tiles' ink splashes paint
    // on the card instead of being hidden behind the background color.
    return Container(
      margin: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
      child: Material(
        color: AppTheme.surface,
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(10),
          side: const BorderSide(color: AppTheme.border, width: 0.5),
        ),
        clipBehavior: Clip.antiAlias,
        child: child,
      ),
    );
  }
}

class _PickerTile extends StatelessWidget {
  final String title;
  final String value;
  final VoidCallback onTap;

  const _PickerTile({
    required this.title,
    required this.value,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    return ListTile(
      onTap: onTap,
      title: Text(title,
          style: const TextStyle(color: AppTheme.textPrimary, fontSize: 15)),
      subtitle: Text(
        value,
        style: const TextStyle(color: AppTheme.textSecondary, fontSize: 13),
        maxLines: 1,
        overflow: TextOverflow.ellipsis,
      ),
      trailing: const Icon(Icons.chevron_right, color: AppTheme.textSecondary),
    );
  }
}
