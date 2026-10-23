import 'package:cantinarr/l10n/l10n.dart';
import 'package:flutter/material.dart';
import '../layout/adaptive.dart';
import '../network/library_settings_service.dart';
import 'error_banner.dart';

/// Author and artist settings retain every untouched field from a fresh read.
/// Chaptarr's independent format gates never become a global monitor toggle.
class LibrarySettingsScreen extends StatefulWidget {
  const LibrarySettingsScreen(
      {super.key,
      required this.service,
      required this.title,
      this.monitoringOnly = false});
  final LibrarySettingsService service;
  final String title;
  final bool monitoringOnly;
  @override
  State<LibrarySettingsScreen> createState() => _LibrarySettingsScreenState();
}

class _LibrarySettingsScreenState extends State<LibrarySettingsScreen> {
  Map<String, dynamic>? _record;
  final _changes = <String, dynamic>{};
  List<Map<String, dynamic>> _quality = [], _metadata = [];
  List<Map<String, dynamic>>? _tags;
  String? _error;
  bool _saving = false;
  bool get _author => widget.service.kind == LibrarySettingsKind.author;
  dynamic _value(String key) =>
      _changes.containsKey(key) ? _changes[key] : _record?[key];
  void _change(String key, dynamic value) => setState(() {
        if (value == _record?[key]) {
          _changes.remove(key);
        } else {
          _changes[key] = value;
        }
      });

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    setState(() => _error = null);
    try {
      final reads = await Future.wait([
        widget.service.read(),
        if (!widget.monitoringOnly) widget.service.options('qualityprofile'),
        if (!widget.monitoringOnly) widget.service.options('metadataprofile'),
      ]);
      List<Map<String, dynamic>>? tags;
      if (!widget.monitoringOnly) {
        try {
          tags = await widget.service.options('tag');
        } catch (_) {/* Preserve tags when unavailable. */}
      }
      if (!mounted) return;
      setState(() {
        _record = reads[0] as Map<String, dynamic>;
        if (!widget.monitoringOnly) {
          _quality = reads[1] as List<Map<String, dynamic>>;
          _metadata = reads[2] as List<Map<String, dynamic>>;
        }
        _tags = tags;
      });
    } catch (e) {
      if (mounted)
        setState(
            () => _error = context.l10n.commonLoadSettingsError(e.toString()));
    }
  }

  Future<void> _save() async {
    setState(() => _saving = true);
    try {
      await widget.service.update(_changes);
      if (mounted) Navigator.pop(context, true);
    } catch (e) {
      if (mounted)
        setState(() {
          _saving = false;
          _error = context.l10n.commonSaveSettingsError(e.toString());
        });
    }
  }

  @override
  Widget build(BuildContext context) => PopScope(
        canPop: !_saving,
        child: Scaffold(
          appBar: AppBar(
              title: Text(widget.monitoringOnly
                  ? context.l10n.commonManageMonitoring
                  : (_author
                      ? context.l10n.commonEditAuthor
                      : context.l10n.commonEditArtist)),
              actions: [
                TextButton(
                    onPressed: _record == null || _saving || _changes.isEmpty
                        ? null
                        : _save,
                    child: Text(_saving
                        ? context.l10n.commonSaving
                        : context.l10n.commonSave)),
              ]),
          body: CenteredContent(
              child: _record == null
                  ? _error == null
                      ? const Center(child: CircularProgressIndicator())
                      : FullScreenError(message: _error!, onRetry: _load)
                  : AbsorbPointer(
                      absorbing: _saving,
                      child: ListView(
                          padding: const EdgeInsets.all(16),
                          children: [
                            Text(widget.title,
                                style: Theme.of(context).textTheme.titleLarge),
                            if (_error != null)
                              ErrorBanner(message: _error!, onRetry: _save),
                            if (_author &&
                                _record!['syncMonitoredAcrossFormats'] == true)
                              Padding(
                                  padding: EdgeInsets.symmetric(vertical: 12),
                                  child:
                                      Text(context.l10n.commonSyncMonitoring)),
                            if (_author) ...[
                              ..._formatSettings(
                                  'ebook', context.l10n.commonEbooks),
                              ..._formatSettings(
                                  'audiobook', context.l10n.commonAudiobooks),
                            ] else ...[
                              _monitor('monitored',
                                  context.l10n.commonMonitorArtist),
                              if (_record!.containsKey('monitorNewItems'))
                                _newItems('monitorNewItems',
                                    context.l10n.commonMonitorNewAlbums),
                              if (!widget.monitoringOnly) ...[
                                _profile(
                                    'qualityProfileId',
                                    context.l10n.commonQualityProfile,
                                    _quality),
                                _profile(
                                    'metadataProfileId',
                                    context.l10n.commonMetadataProfile,
                                    _metadata),
                                _tagPicker('tags', context.l10n.commonTags),
                              ],
                            ],
                            if (!widget.monitoringOnly && _tags == null)
                              Padding(
                                  padding: EdgeInsets.all(16),
                                  child:
                                      Text(context.l10n.commonTagsLoadWarning)),
                          ]))),
        ),
      );

  List<Widget> _formatSettings(String format, String label) => [
        Padding(
            padding: const EdgeInsets.only(top: 24, bottom: 8),
            child: Text(label, style: Theme.of(context).textTheme.titleMedium)),
        if (_record!.containsKey('${format}Monitored')) ...[
          _monitor('${format}Monitored',
              context.l10n.commonMonitorFormat(label.toLowerCase())),
          if (_record!.containsKey('${format}MonitorNewItems'))
            _newItems('${format}MonitorNewItems',
                context.l10n.commonMonitorNewFormat(label.toLowerCase())),
        ] else
          ListTile(
              title: Text(context.l10n.commonMonitoringUnavailable),
              subtitle: Text(context.l10n.commonMonitoringFormatWarning)),
        if (!widget.monitoringOnly) ...[
          if (_record!.containsKey('${format}QualityProfileId'))
            _profile('${format}QualityProfileId',
                context.l10n.commonQualityProfile, _quality,
                format: format),
          if (_record!.containsKey('${format}MetadataProfileId'))
            _profile('${format}MetadataProfileId',
                context.l10n.commonMetadataProfile, _metadata,
                format: format),
          if (_record!.containsKey('${format}Tags'))
            _tagPicker('${format}Tags', context.l10n.commonTags),
        ],
      ];

  Widget _monitor(String key, String label) => SwitchListTile(
        key: ValueKey(key),
        title: Text(label),
        value: _value(key) == true,
        subtitle: Text(_value(key) == null
            ? context.l10n.commonNotConfiguredLibrary
            : (_author
                ? context.l10n.commonMonitorBookExplanation
                : context.l10n.commonMonitorAlbumExplanation)),
        onChanged: (value) => _change(key, value),
      );

  Widget _newItems(String key, String label) {
    final value = _value(key);
    final choices = <String, String>{
      'all': context.l10n.commonAll,
      'new': context.l10n.commonNewReleasesOnly,
      'none': context.l10n.commonNone
    };
    return ListTile(
        key: ValueKey(key),
        title: Text(label),
        subtitle: Text(choices[value] ??
            (value == null ? context.l10n.commonNotConfigured : '$value')),
        trailing: const Icon(Icons.chevron_right),
        onTap: () async {
          final selected = await showDialog<String>(
              context: context,
              builder: (ctx) => SimpleDialog(title: Text(label), children: [
                    for (final entry in choices.entries)
                      SimpleDialogOption(
                          onPressed: () => Navigator.pop(ctx, entry.key),
                          child: Text(entry.value))
                  ]));
          if (selected != null && mounted) _change(key, selected);
        });
  }

  Widget _profile(String key, String label, List<Map<String, dynamic>> all,
      {String? format}) {
    final choices = all.where((p) {
      final type = p['profileType']?.toString().toLowerCase();
      return format == null ||
          type == null ||
          type == format ||
          type == (format == 'ebook' ? '2' : '1');
    }).toList();
    final value = _value(key);
    final names = {for (final p in choices) p['id']: p['name']};
    return ListTile(
        key: ValueKey(key),
        title: Text(label),
        subtitle: Text(names[value]?.toString() ??
            (value == null || value == 0
                ? context.l10n.commonNotConfigured
                : context.l10n.commonUnavailableProfile(value.toString()))),
        trailing: const Icon(Icons.chevron_right),
        onTap: () async {
          final selected = await showDialog<int>(
              context: context,
              builder: (ctx) => SimpleDialog(
                  title: Text(label),
                  children: choices.isEmpty
                      ? [
                          Padding(
                              padding: EdgeInsets.all(24),
                              child:
                                  Text(context.l10n.commonNoCompatibleProfiles))
                        ]
                      : [
                          for (final p in choices)
                            SimpleDialogOption(
                                onPressed: () => Navigator.pop(ctx, p['id']),
                                child: Text('${p['name']}'))
                        ]));
          if (selected != null && mounted) _change(key, selected);
        });
  }

  Widget _tagPicker(String key, String label) {
    if (_tags == null) return const SizedBox.shrink();
    final ids = List<int>.from(_value(key) as List? ?? []);
    final names = {for (final t in _tags!) t['id']: t['label']};
    return ListTile(
        key: ValueKey(key),
        title: Text(label),
        subtitle: Text(ids.isEmpty
            ? context.l10n.commonNone
            : ids
                .map((id) => names[id] ?? context.l10n.commonTag(id.toString()))
                .join(', ')),
        trailing: const Icon(Icons.chevron_right),
        onTap: () async {
          final selected = {...ids};
          final saved = await showDialog<bool>(
              context: context,
              builder: (ctx) => StatefulBuilder(
                  builder: (ctx, update) => AlertDialog(
                          title: Text(label),
                          content: SingleChildScrollView(
                              child: Column(
                                  mainAxisSize: MainAxisSize.min,
                                  children: [
                                for (final id in {
                                  ...names.keys.cast<int>(),
                                  ...ids
                                })
                                  CheckboxListTile(
                                      title: Text(
                                          '${names[id] ?? context.l10n.commonUnavailableTag(id.toString())}'),
                                      value: selected.contains(id),
                                      onChanged: (on) => update(() {
                                            if (on == true) {
                                              selected.add(id);
                                            } else {
                                              selected.remove(id);
                                            }
                                          }))
                              ])),
                          actions: [
                            TextButton(
                                onPressed: () => Navigator.pop(ctx),
                                child: Text(context.l10n.commonCancel)),
                            TextButton(
                                onPressed: () => Navigator.pop(ctx, true),
                                child: Text(context.l10n.commonDone))
                          ])));
          if (saved == true && mounted) _change(key, selected.toList()..sort());
        });
  }
}
