import 'package:flutter/material.dart';
import '../layout/adaptive.dart';
import '../network/library_settings_service.dart';
import 'error_banner.dart';

/// Author and artist settings retain every untouched field from a fresh read.
/// Chaptarr's independent format gates never become a global monitor toggle.
class LibrarySettingsScreen extends StatefulWidget {
  const LibrarySettingsScreen({super.key, required this.service,
    required this.title, this.monitoringOnly = false});
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
  dynamic _value(String key) => _changes.containsKey(key) ? _changes[key] : _record?[key];
  void _change(String key, dynamic value) => setState(() {
    if (value == _record?[key]) { _changes.remove(key); } else { _changes[key] = value; }
  });

  @override
  void initState() { super.initState(); _load(); }

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
        try { tags = await widget.service.options('tag'); } catch (_) { /* Preserve tags when unavailable. */ }
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
      if (mounted) setState(() => _error = 'Could not load settings: $e');
    }
  }

  Future<void> _save() async {
    setState(() => _saving = true);
    try {
      await widget.service.update(_changes);
      if (mounted) Navigator.pop(context, true);
    } catch (e) {
      if (mounted) setState(() { _saving = false; _error = 'Could not save settings: $e'; });
    }
  }

  @override
  Widget build(BuildContext context) => PopScope(
    canPop: !_saving,
    child: Scaffold(
      appBar: AppBar(title: Text(widget.monitoringOnly ? 'Manage monitoring' :
          'Edit ${_author ? 'author' : 'artist'}'), actions: [
        TextButton(onPressed: _record == null || _saving || _changes.isEmpty ? null : _save,
          child: Text(_saving ? 'Saving…' : 'Save')),
      ]),
      body: CenteredContent(child: _record == null
        ? _error == null ? const Center(child: CircularProgressIndicator())
            : FullScreenError(message: _error!, onRetry: _load)
        : AbsorbPointer(absorbing: _saving, child: ListView(padding: const EdgeInsets.all(16), children: [
          Text(widget.title, style: Theme.of(context).textTheme.titleLarge),
          if (_error != null) ErrorBanner(message: _error!, onRetry: _save),
          if (_author && _record!['syncMonitoredAcrossFormats'] == true)
            const Padding(padding: EdgeInsets.symmetric(vertical: 12), child: Text(
              'Chaptarr is set to sync book monitoring between formats. Its sync rules still apply.')),
          if (_author) ...[
            ..._formatSettings('ebook', 'eBooks'),
            ..._formatSettings('audiobook', 'Audiobooks'),
          ] else ...[
            _monitor('monitored', 'Monitor artist'),
            if (_record!.containsKey('monitorNewItems'))
              _newItems('monitorNewItems', 'Monitor new albums'),
            if (!widget.monitoringOnly) ...[
              _profile('qualityProfileId', 'Quality profile', _quality),
              _profile('metadataProfileId', 'Metadata profile', _metadata),
              _tagPicker('tags', 'Tags'),
            ],
          ],
          if (!widget.monitoringOnly && _tags == null)
            const Padding(padding: EdgeInsets.all(16), child: Text(
              'Tags could not be loaded. Your existing tags will be kept.')),
        ]))),
    ),
  );

  List<Widget> _formatSettings(String format, String label) => [
    Padding(padding: const EdgeInsets.only(top: 24, bottom: 8),
      child: Text(label, style: Theme.of(context).textTheme.titleMedium)),
    if (_record!.containsKey('${format}Monitored')) ...[
      _monitor('${format}Monitored', 'Monitor ${format == 'audiobook' ? 'audiobooks' : label}'),
      if (_record!.containsKey('${format}MonitorNewItems'))
        _newItems('${format}MonitorNewItems', 'Monitor new ${format == 'audiobook' ? 'audiobooks' : label}'),
    ] else
      const ListTile(title: Text('Monitoring settings unavailable'),
        subtitle: Text('This Chaptarr version does not expose separate format gates. Manage monitoring in Chaptarr.')),
    if (!widget.monitoringOnly) ...[
      if (_record!.containsKey('${format}QualityProfileId'))
        _profile('${format}QualityProfileId', 'Quality profile', _quality, format: format),
      if (_record!.containsKey('${format}MetadataProfileId'))
        _profile('${format}MetadataProfileId', 'Metadata profile', _metadata, format: format),
      if (_record!.containsKey('${format}Tags')) _tagPicker('${format}Tags', 'Tags'),
    ],
  ];

  Widget _monitor(String key, String label) => SwitchListTile(
    key: ValueKey(key), title: Text(label), value: _value(key) == true,
    subtitle: Text(_value(key) == null ? 'Not configured in this library'
        : 'Pause or resume searches. Existing ${_author ? 'book' : 'album'} selections are kept.'),
    onChanged: (value) => _change(key, value),
  );

  Widget _newItems(String key, String label) {
    final value = _value(key);
    final choices = <String, String>{'all': 'All', 'new': 'New releases only', 'none': 'None'};
    return ListTile(key: ValueKey(key), title: Text(label),
      subtitle: Text(choices[value] ?? (value == null ? 'Not configured' : '$value')),
      trailing: const Icon(Icons.chevron_right),
      onTap: () async {
        final selected = await showDialog<String>(context: context, builder: (ctx) => SimpleDialog(
          title: Text(label), children: [for (final entry in choices.entries)
            SimpleDialogOption(onPressed: () => Navigator.pop(ctx, entry.key), child: Text(entry.value))]));
        if (selected != null && mounted) _change(key, selected);
      });
  }

  Widget _profile(String key, String label, List<Map<String, dynamic>> all, {String? format}) {
    final choices = all.where((p) {
      final type = p['profileType']?.toString().toLowerCase();
      return format == null || type == null || type == format ||
          type == (format == 'ebook' ? '2' : '1');
    }).toList();
    final value = _value(key);
    final names = {for (final p in choices) p['id']: p['name']};
    return ListTile(key: ValueKey(key), title: Text(label),
      subtitle: Text(names[value]?.toString() ?? (value == null || value == 0
          ? 'Not configured' : 'Profile $value (unavailable)')),
      trailing: const Icon(Icons.chevron_right),
      onTap: () async {
        final selected = await showDialog<int>(context: context, builder: (ctx) => SimpleDialog(
          title: Text(label), children: choices.isEmpty
            ? [const Padding(padding: EdgeInsets.all(24), child: Text('No compatible profiles available.'))]
            : [for (final p in choices) SimpleDialogOption(
                onPressed: () => Navigator.pop(ctx, p['id']), child: Text('${p['name']}'))]));
        if (selected != null && mounted) _change(key, selected);
      });
  }

  Widget _tagPicker(String key, String label) {
    if (_tags == null) return const SizedBox.shrink();
    final ids = List<int>.from(_value(key) as List? ?? []);
    final names = {for (final t in _tags!) t['id']: t['label']};
    return ListTile(key: ValueKey(key), title: Text(label),
      subtitle: Text(ids.isEmpty ? 'None' : ids.map((id) => names[id] ?? 'Tag $id').join(', ')),
      trailing: const Icon(Icons.chevron_right),
      onTap: () async {
        final selected = {...ids};
        final saved = await showDialog<bool>(context: context, builder: (ctx) => StatefulBuilder(
          builder: (ctx, update) => AlertDialog(title: Text(label),
            content: SingleChildScrollView(child: Column(mainAxisSize: MainAxisSize.min,
              children: [for (final id in {...names.keys.cast<int>(), ...ids})
                CheckboxListTile(title: Text('${names[id] ?? 'Tag $id (unavailable)'}'),
                  value: selected.contains(id), onChanged: (on) => update(() {
                    if (on == true) { selected.add(id); } else { selected.remove(id); }
                  }))])),
            actions: [TextButton(onPressed: () => Navigator.pop(ctx), child: const Text('Cancel')),
              TextButton(onPressed: () => Navigator.pop(ctx, true), child: const Text('Done'))])));
        if (saved == true && mounted) _change(key, selected.toList()..sort());
      });
  }
}
