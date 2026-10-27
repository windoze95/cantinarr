import 'package:flutter/material.dart';

import '../../../core/theme/app_theme.dart';
import '../../../l10n/l10n.dart';
import '../data/app_icon_service.dart';

class AppIconSheet extends StatefulWidget {
  const AppIconSheet({super.key});

  @override
  State<AppIconSheet> createState() => _AppIconSheetState();
}

class _AppIconSheetState extends State<AppIconSheet> {
  final _service = AppIconService();
  AppIcon? _selected;
  bool _loading = true;
  bool _saving = false;
  bool _supported = true;
  bool _failed = false;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    setState(() {
      _loading = true;
      _failed = false;
    });
    try {
      final supported = await _service.isSupported();
      final icon = supported ? await _service.current() : null;
      if (!mounted) return;
      setState(() {
        _supported = supported;
        _selected = icon;
      });
    } catch (_) {
      if (mounted) setState(() => _failed = true);
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  Future<void> _select(AppIcon icon) async {
    if (_saving || icon == _selected) return;
    setState(() {
      _saving = true;
      _failed = false;
    });
    try {
      final selected = await _service.select(icon);
      if (mounted) setState(() => _selected = selected);
    } catch (_) {
      // Reconcile after any uncertain result instead of claiming it changed.
      try {
        final actual = await _service.current();
        if (mounted) setState(() => _selected = actual);
      } catch (_) {}
      if (mounted) setState(() => _failed = true);
    } finally {
      if (mounted) setState(() => _saving = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final l10n = context.l10n;
    return SafeArea(
      child: SingleChildScrollView(
        padding: const EdgeInsets.fromLTRB(16, 8, 16, 24),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Text(l10n.settingsAppIcon,
                style: Theme.of(context).textTheme.titleLarge),
            const SizedBox(height: 8),
            Text(l10n.settingsAppIconHelp),
            const SizedBox(height: 16),
            if (_loading)
              const Center(child: CircularProgressIndicator())
            else if (!_supported)
              Text(l10n.settingsAppIconUnsupported)
            else ...[
              for (final icon in AppIcon.values)
                ListTile(
                  contentPadding:
                      const EdgeInsets.symmetric(vertical: 4, horizontal: 8),
                  leading: ClipRRect(
                    borderRadius: BorderRadius.circular(12),
                    child: Image.asset('assets/${icon.asset}',
                        width: 48, height: 48),
                  ),
                  title: Text(icon == AppIcon.original
                      ? l10n.settingsAppIconOriginal
                      : icon.label),
                  subtitle: icon == AppIcon.playSun
                      ? Text(l10n.settingsAppIconDefault)
                      : null,
                  selected: icon == _selected,
                  enabled: !_saving && _selected != null,
                  trailing: icon == _selected
                      ? const Icon(Icons.check_circle, color: AppTheme.accent)
                      : null,
                  onTap: () => _select(icon),
                ),
              if (_saving) const LinearProgressIndicator(),
            ],
            if (_failed) ...[
              const SizedBox(height: 12),
              Text(l10n.settingsAppIconError,
                  style: const TextStyle(color: AppTheme.error)),
              if (_selected == null)
                TextButton(onPressed: _load, child: Text(l10n.commonRetry)),
            ],
          ],
        ),
      ),
    );
  }
}
