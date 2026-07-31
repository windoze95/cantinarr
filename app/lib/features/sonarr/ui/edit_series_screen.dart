import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/layout/adaptive.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/error_banner.dart';
import '../data/sonarr_api_service.dart';
import '../data/sonarr_models.dart';

/// Edit a series' Sonarr settings: monitored, season folders, quality profile,
/// series type, path and tags. Saving PUTs the whole series resource back with
/// only these fields changed. Pops `true` after a successful update so callers
/// can reload. Admin only (the proxy requires instances:manage for the PUT).
class EditSeriesScreen extends ConsumerStatefulWidget {
  final String instanceId;
  final SonarrSeries series;

  const EditSeriesScreen({
    super.key,
    required this.instanceId,
    required this.series,
  });

  @override
  ConsumerState<EditSeriesScreen> createState() => _EditSeriesScreenState();
}

class _EditSeriesScreenState extends ConsumerState<EditSeriesScreen> {
  static const _seriesTypes = ['standard', 'daily', 'anime'];

  late final SonarrApiService _service;
  bool _isLoading = true;
  bool _isSaving = false;
  String? _error;

  List<SonarrQualityProfile> _profiles = [];
  // Null when the tag list couldn't be loaded — the Tags row is hidden then.
  List<SonarrTag>? _allTags;

  late bool _monitored;
  late bool _seasonFolder;
  late int _qualityProfileId;
  late String _seriesType;
  late String _path;
  late Set<int> _tagIds;

  @override
  void initState() {
    super.initState();
    _service = SonarrApiService(
      backendDio: ref.read(backendClientProvider),
      instanceId: widget.instanceId,
    );
    _seedFrom(widget.series);
    WidgetsBinding.instance.addPostFrameCallback((_) => _load());
  }

  void _seedFrom(SonarrSeries s) {
    _monitored = s.monitored;
    _seasonFolder = s.seasonFolder;
    _qualityProfileId = s.qualityProfileId;
    _seriesType = s.seriesType;
    _path = s.path ?? '';
    _tagIds = {...s.tags};
  }

  Future<void> _load() async {
    setState(() => _isLoading = true);
    try {
      // Profiles and the fresh series are required; tags are optional.
      final profilesFuture = _service.getQualityProfiles();
      final seriesFuture = _service.getSeriesById(widget.series.id);
      final profiles = await profilesFuture;
      final series = await seriesFuture;
      List<SonarrTag>? tags;
      try {
        tags = await _service.getTags();
      } catch (_) {
        tags = null;
      }
      if (!mounted) return;
      setState(() {
        _profiles = profiles;
        _allTags = tags;
        _seedFrom(series);
        _isLoading = false;
        _error = null;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _isLoading = false;
        _error = 'Failed to load series settings: $e';
      });
    }
  }

  Future<void> _save() async {
    setState(() => _isSaving = true);
    try {
      await _service.updateSeriesFields(widget.series.id, {
        'monitored': _monitored,
        'seasonFolder': _seasonFolder,
        'qualityProfileId': _qualityProfileId,
        'seriesType': _seriesType,
        'path': _path,
        'tags': _tagIds.toList()..sort(),
      });
      if (!mounted) return;
      ScaffoldMessenger.of(context)
          .showSnackBar(const SnackBar(content: Text('Series updated')));
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

  Future<void> _pickSeriesType() async {
    final picked = await showDialog<String>(
      context: context,
      builder: (ctx) => SimpleDialog(
        backgroundColor: AppTheme.surface,
        title: const Text('Series Type'),
        children: _seriesTypes
            .map((t) => SimpleDialogOption(
                  onPressed: () => Navigator.pop(ctx, t),
                  child: Text(
                    t[0].toUpperCase() + t.substring(1),
                    style: TextStyle(
                      color: t == _seriesType
                          ? AppTheme.accent
                          : AppTheme.textPrimary,
                      fontSize: 15,
                    ),
                  ),
                ))
            .toList(),
      ),
    );
    if (picked != null) setState(() => _seriesType = picked);
  }

  Future<void> _editPath() async {
    final controller = TextEditingController(text: _path);
    final picked = await showDialog<String>(
      context: context,
      builder: (ctx) => AlertDialog(
        backgroundColor: AppTheme.surface,
        title: const Text('Series Path'),
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
                ? const Text('No tags defined in Sonarr.',
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
        title: const Text('Edit Series'),
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
                child: SwitchListTile(
                  value: _seasonFolder,
                  onChanged: (v) => setState(() => _seasonFolder = v),
                  title: const Text('Use Season Folders',
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
                  title: 'Series Type',
                  value:
                      _seriesType[0].toUpperCase() + _seriesType.substring(1),
                  onTap: _pickSeriesType,
                ),
              ),
              _SettingCard(
                child: _PickerTile(
                  title: 'Series Path',
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
