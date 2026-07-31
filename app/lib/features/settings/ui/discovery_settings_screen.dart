import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/layout/adaptive.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/status_pill.dart';
import '../data/discovery_settings_service.dart';

/// Admin screen for choosing which feed backs the headline discovery rows and
/// whether those rows drop non-English titles.
class DiscoverySettingsScreen extends ConsumerStatefulWidget {
  const DiscoverySettingsScreen({super.key});

  @override
  ConsumerState<DiscoverySettingsScreen> createState() =>
      _DiscoverySettingsScreenState();
}

class _DiscoverySettingsScreenState
    extends ConsumerState<DiscoverySettingsScreen> {
  late final DiscoverySettingsService _service;

  DiscoverySettings? _edited;
  bool _isLoading = true;
  String? _error;
  bool _saving = false;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      _service = DiscoverySettingsService(
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
      _isLoading = _edited == null;
      _error = null;
    });
    try {
      final settings = await _service.get();
      if (!mounted) return;
      setState(() {
        _edited = settings;
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
      final saved = await _service.update(edited);
      if (!mounted) return;
      setState(() {
        _edited = saved;
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
      appBar: AppBar(title: const Text('Discovery')),
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
                source.label,
                style: TextStyle(
                  color:
                      selectable ? AppTheme.textPrimary : AppTheme.textSecondary,
                  fontWeight: FontWeight.w500,
                ),
              ),
            ),
            if (source.recommended) ...[
              const SizedBox(width: 8),
              StatusPill(
                text: 'Recommended',
                color: selectable ? AppTheme.accent : AppTheme.textSecondary,
              ),
            ],
          ],
        ),
        subtitle: Text(
          selectable
              ? source.description
              : 'Add a Trakt client ID under Credentials to use this.',
          style: const TextStyle(color: AppTheme.textSecondary, fontSize: 13),
        ),
      ),
    );
  }

  Widget _buildBody(DiscoverySettings edited) {
    return ListView(
      padding: const EdgeInsets.symmetric(vertical: 8),
      children: [
        const Padding(
          padding: EdgeInsets.fromLTRB(16, 8, 16, 4),
          child: Text(
            'These settings shape the headline row on the Movies and TV tabs, '
            'and the recommendation rows throughout the app. Search is never '
            'filtered.',
            style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
          ),
        ),
        const _SectionLabel('Row source'),
        for (final source in edited.sources)
          _sourceTile(edited, source),
        const _SectionLabel('Language'),
        SwitchListTile(
          value: edited.englishOnly,
          activeThumbColor: AppTheme.accent,
          onChanged: (v) =>
              setState(() => _edited = edited.copyWith(englishOnly: v)),
          title: const Text(
            'Only show English-language titles',
            style: TextStyle(
                color: AppTheme.textPrimary, fontWeight: FontWeight.w500),
          ),
          subtitle: const Text(
            'Hides titles whose original language is not English from the '
            'discovery and recommendation rows. Search still finds everything.',
            style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
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
                  : const Text('Save'),
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
