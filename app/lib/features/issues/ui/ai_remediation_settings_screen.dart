import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/layout/adaptive.dart';
import '../../../core/theme/app_theme.dart';
import '../data/issue_models.dart';
import '../logic/issues_provider.dart';

/// Admin screen for the AI-remediation settings. Clones
/// `RequestSettingsScreen`'s load → edit → Save shape: a master Enabled
/// switch, sub-toggles, an autonomy dropdown, free-text provider/model
/// overrides, and numeric bound fields.
class AiRemediationSettingsScreen extends ConsumerStatefulWidget {
  const AiRemediationSettingsScreen({super.key});

  @override
  ConsumerState<AiRemediationSettingsScreen> createState() =>
      _AiRemediationSettingsScreenState();
}

class _AiRemediationSettingsScreenState
    extends ConsumerState<AiRemediationSettingsScreen> {
  RemediationSettings? _edited;
  bool _isLoading = true;
  String? _error;
  bool _saving = false;

  // Free-text controllers for provider/model so a blank means "server
  // default". The agent is provider-agnostic — no fixed dropdown here.
  final _providerController = TextEditingController();
  final _modelController = TextEditingController();

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) => _load());
  }

  @override
  void dispose() {
    _providerController.dispose();
    _modelController.dispose();
    super.dispose();
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
      final settings = await ref.read(issuesServiceProvider).getSettings();
      if (!mounted) return;
      setState(() {
        _edited = settings;
        _providerController.text = settings.provider;
        _modelController.text = settings.model;
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
      final toSave = edited.copyWith(
        provider: _providerController.text.trim(),
        model: _modelController.text.trim(),
      );
      final saved =
          await ref.read(issuesServiceProvider).updateSettings(toSave);
      if (!mounted) return;
      setState(() {
        _edited = saved;
        _providerController.text = saved.provider;
        _modelController.text = saved.model;
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
      appBar: AppBar(title: const Text('AI Remediation')),
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
                                _friendlyError(
                                    _error ?? 'Something went wrong'),
                                style: const TextStyle(color: AppTheme.error),
                                textAlign: TextAlign.center),
                            const SizedBox(height: 12),
                            ElevatedButton(
                                onPressed: _load, child: const Text('Retry')),
                          ],
                        ),
                      ),
                    )
                  : _buildBody(_edited!)),
    );
  }

  Widget _buildBody(RemediationSettings s) {
    return ListView(
      padding: const EdgeInsets.symmetric(vertical: 8),
      children: [
        const Padding(
          padding: EdgeInsets.fromLTRB(16, 8, 16, 4),
          child: Text(
            'Let the assistant investigate reported problems and propose fixes '
            'for your approval. Read-only investigation never changes anything; '
            'every mutation waits for an admin.',
            style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
          ),
        ),
        const _SectionLabel('General'),
        SwitchListTile(
          value: s.enabled,
          activeThumbColor: AppTheme.accent,
          onChanged: (v) => setState(() => _edited = s.copyWith(enabled: v)),
          title: const Text(
            'Enabled',
            style: TextStyle(
                color: AppTheme.textPrimary, fontWeight: FontWeight.w500),
          ),
          subtitle: const Text(
            'Master switch for the remediation assistant.',
            style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
          ),
        ),
        SwitchListTile(
          value: s.autoDispatch,
          activeThumbColor: AppTheme.accent,
          onChanged: (v) =>
              setState(() => _edited = s.copyWith(autoDispatch: v)),
          title: const Text(
            'Auto-dispatch on detected problems',
            style: TextStyle(
                color: AppTheme.textPrimary, fontWeight: FontWeight.w500),
          ),
          subtitle: const Text(
            'Open and investigate issues automatically for stuck downloads.',
            style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
          ),
        ),
        SwitchListTile(
          value: s.allowReporting,
          activeThumbColor: AppTheme.accent,
          onChanged: (v) =>
              setState(() => _edited = s.copyWith(allowReporting: v)),
          title: const Text(
            'Allow problem reporting',
            style: TextStyle(
                color: AppTheme.textPrimary, fontWeight: FontWeight.w500),
          ),
          subtitle: const Text(
            'Show a "Report a problem" button to users on media screens.',
            style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
          ),
        ),
        ListTile(
          title: const Text(
            'Autonomy',
            style: TextStyle(
                color: AppTheme.textPrimary, fontWeight: FontWeight.w500),
          ),
          subtitle: const Text(
            'How far the assistant may go on its own.',
            style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
          ),
          trailing: DropdownButton<RemediationAutonomy>(
            value: s.autonomy,
            dropdownColor: AppTheme.surface,
            underline: const SizedBox.shrink(),
            style: const TextStyle(color: AppTheme.textPrimary, fontSize: 14),
            items: [
              for (final a in RemediationAutonomy.values)
                DropdownMenuItem<RemediationAutonomy>(
                  value: a,
                  child: Text(a.label),
                ),
            ],
            onChanged: (v) {
              if (v == null) return;
              setState(() => _edited = s.copyWith(autonomy: v));
            },
          ),
        ),
        const _SectionLabel('Model'),
        _TextField(
          controller: _providerController,
          label: 'AI provider',
          help: "Leave blank to use the server's configured AI provider.",
          hint: 'e.g. anthropic, openai',
        ),
        _TextField(
          controller: _modelController,
          label: 'Model',
          help: "Leave blank to use the server's configured model.",
          hint: 'e.g. a model name',
        ),
        const _SectionLabel('Limits'),
        _NumberTile(
          label: 'Max steps per run',
          value: s.maxSteps,
          onChanged: (v) => setState(() => _edited = s.copyWith(maxSteps: v)),
        ),
        _NumberTile(
          label: 'Max wall-clock (seconds)',
          value: s.maxWallClockSecs,
          onChanged: (v) =>
              setState(() => _edited = s.copyWith(maxWallClockSecs: v)),
        ),
        _NumberTile(
          label: 'Max cost per run (micros)',
          value: s.maxCostMicros,
          onChanged: (v) =>
              setState(() => _edited = s.copyWith(maxCostMicros: v)),
        ),
        _NumberTile(
          label: 'Daily run cap',
          value: s.dailyRunCap,
          onChanged: (v) =>
              setState(() => _edited = s.copyWith(dailyRunCap: v)),
        ),
        _NumberTile(
          label: 'Daily cost ceiling (micros)',
          value: s.dailyCostCeilingMicros,
          onChanged: (v) =>
              setState(() => _edited = s.copyWith(dailyCostCeilingMicros: v)),
        ),
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 24, 16, 16),
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
                        color: Colors.white,
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

/// A labelled free-text field with helper text (used for provider/model).
class _TextField extends StatelessWidget {
  final TextEditingController controller;
  final String label;
  final String help;
  final String hint;

  const _TextField({
    required this.controller,
    required this.label,
    required this.help,
    required this.hint,
  });

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 8),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            label,
            style: const TextStyle(
                color: AppTheme.textPrimary, fontWeight: FontWeight.w500),
          ),
          const SizedBox(height: 6),
          TextField(
            controller: controller,
            style: const TextStyle(color: AppTheme.textPrimary),
            decoration: InputDecoration(
              isDense: true,
              hintText: hint,
              hintStyle: const TextStyle(color: AppTheme.textSecondary),
              helperText: help,
              helperStyle:
                  const TextStyle(color: AppTheme.textSecondary, fontSize: 12),
              helperMaxLines: 2,
              border: OutlineInputBorder(
                borderRadius: BorderRadius.circular(10),
              ),
            ),
          ),
        ],
      ),
    );
  }
}

/// A labelled integer field. Empty input is treated as 0.
class _NumberTile extends StatefulWidget {
  final String label;
  final int value;
  final ValueChanged<int> onChanged;

  const _NumberTile({
    required this.label,
    required this.value,
    required this.onChanged,
  });

  @override
  State<_NumberTile> createState() => _NumberTileState();
}

class _NumberTileState extends State<_NumberTile> {
  late final TextEditingController _controller;

  @override
  void initState() {
    super.initState();
    _controller = TextEditingController(text: widget.value.toString());
  }

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return ListTile(
      title: Text(
        widget.label,
        style: const TextStyle(
            color: AppTheme.textPrimary, fontWeight: FontWeight.w500),
      ),
      trailing: SizedBox(
        width: 120,
        child: TextField(
          controller: _controller,
          textAlign: TextAlign.end,
          keyboardType: TextInputType.number,
          inputFormatters: [FilteringTextInputFormatter.digitsOnly],
          style: const TextStyle(color: AppTheme.textPrimary),
          decoration: const InputDecoration(
            isDense: true,
            border: OutlineInputBorder(),
          ),
          onChanged: (v) => widget.onChanged(int.tryParse(v) ?? 0),
        ),
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
