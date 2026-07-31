import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/layout/adaptive.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/theme/app_theme.dart';
import '../../settings/data/credentials_service.dart';
import '../data/issue_models.dart';
import '../logic/issues_provider.dart';

/// Admin screen for the AI-remediation settings. Clones
/// `RequestSettingsScreen`'s load → edit → Save shape: a master Enabled
/// switch, sub-toggles, a remediation-mode dropdown, and numeric bound fields.
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
  int _loadEpoch = 0;
  CredentialsStatus? _credentials;
  String _modelSelection = _sharedModel;
  final _customModelController = TextEditingController();

  static const _sharedModel = '__shared__';
  static const _customModel = '__custom__';

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) => _load());
  }

  @override
  void dispose() {
    _customModelController.dispose();
    super.dispose();
  }

  String _friendlyError(Object e) {
    final m = RegExp(r'"error":"([^"]+)"').firstMatch(e.toString());
    return m != null ? m.group(1)! : 'Something went wrong';
  }

  Future<void> _load() async {
    final epoch = ++_loadEpoch;
    setState(() {
      _isLoading = _edited == null;
      _error = null;
    });
    try {
      final results = await Future.wait<Object>([
        ref.read(issuesServiceProvider).getSettings(),
        CredentialsService(backendDio: ref.read(backendClientProvider))
            .getStatus(),
      ]);
      final settings = results[0] as RemediationSettings;
      final credentials = results[1] as CredentialsStatus;
      if (!mounted || epoch != _loadEpoch) return;
      setState(() {
        _edited = settings;
        _credentials = credentials;
        _syncModelSelection(settings, credentials);
        _isLoading = false;
      });
    } catch (e) {
      if (!mounted || epoch != _loadEpoch) return;
      setState(() {
        _error = e.toString();
        _isLoading = false;
      });
    }
  }

  Future<void> _save() async {
    final edited = _edited;
    final credentials = _credentials;
    if (edited == null || credentials == null || _saving) return;
    final modelOverride = switch (_modelSelection) {
      _sharedModel => '',
      _customModel => _customModelController.text.trim(),
      _ => _modelSelection,
    };
    if (_modelSelection == _customModel && modelOverride.isEmpty) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Enter a custom model ID')),
      );
      return;
    }
    setState(() => _saving = true);
    try {
      final toSave = edited.copyWith(
        // Legacy fields remain empty. The new override changes only the model;
        // its provider binding is verified and overwritten by the server.
        provider: '',
        model: '',
        modelOverride: modelOverride,
        modelOverrideProvider:
            modelOverride.isEmpty ? '' : credentials.ai.provider,
      );
      final saved =
          await ref.read(issuesServiceProvider).updateSettings(toSave);
      if (!mounted) return;
      setState(() {
        _edited = saved;
        _syncModelSelection(saved, credentials);
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

  AiProviderOption? _providerOption(CredentialsStatus credentials) {
    for (final option in credentials.ai.providers) {
      if (option.id == credentials.ai.provider) return option;
    }
    return null;
  }

  void _syncModelSelection(
    RemediationSettings settings,
    CredentialsStatus credentials,
  ) {
    final option = _providerOption(credentials);
    final activeOverride =
        settings.modelOverrideProvider == credentials.ai.provider
            ? settings.modelOverride
            : '';
    if (activeOverride.isEmpty) {
      _modelSelection = _sharedModel;
      return;
    }
    if (option?.models.any((model) => model.id == activeOverride) == true) {
      _modelSelection = activeOverride;
      return;
    }
    _modelSelection = _customModel;
    _customModelController.text = activeOverride;
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
            'every mutation waits for an admin. This server-owned agent always '
            'uses the admin shared provider, so runs count against its API quota '
            'or shared OpenAI OAuth usage meter. Personal providers and per-user AI '
            'access switches are never used.',
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
            'Track detected problems quietly while Radarr or Sonarr retries, '
            'then investigate only after the observation window.',
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
        SwitchListTile(
          value: s.markResolvedAsRead,
          activeThumbColor: AppTheme.accent,
          onChanged: (v) =>
              setState(() => _edited = s.copyWith(markResolvedAsRead: v)),
          title: const Text(
            'Mark resolved issues as read',
            style: TextStyle(
                color: AppTheme.textPrimary, fontWeight: FontWeight.w500),
          ),
          subtitle: const Text(
            'Clear the unread dot when an issue resolves, instead of re-flagging '
            'it for another look.',
            style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
          ),
        ),
        ListTile(
          title: const Text(
            'Mode',
            style: TextStyle(
                color: AppTheme.textPrimary, fontWeight: FontWeight.w500),
          ),
          subtitle: const Text(
            'Whether the assistant may prepare a fix for an admin to review.',
            style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
          ),
          trailing: DropdownButton<RemediationMode>(
            value: s.mode,
            dropdownColor: AppTheme.surface,
            underline: const SizedBox.shrink(),
            style: const TextStyle(color: AppTheme.textPrimary, fontSize: 14),
            items: [
              for (final a in RemediationMode.values)
                DropdownMenuItem<RemediationMode>(
                  value: a,
                  child: Text(a.label),
                ),
            ],
            onChanged: (v) {
              if (v == null) return;
              setState(() => _edited = s.copyWith(mode: v));
            },
          ),
        ),
        const _SectionLabel('Automatic recovery'),
        const Padding(
          padding: EdgeInsets.fromLTRB(16, 4, 16, 8),
          child: Text(
            'Detected download problems stay silent while the arr may still '
            'recover on its own. These timers decide when recovery has had '
            'enough time and the issue should become actionable.',
            style: TextStyle(
              color: AppTheme.textSecondary,
              fontSize: 13,
              height: 1.35,
            ),
          ),
        ),
        _NumberTile(
          label: 'Minimum watch time (minutes)',
          help: 'Wait at least this long before any agent work, proposal, or '
              'admin alert can begin.',
          value: s.observationMinMinutes,
          onChanged: (v) => setState(
            () => _edited = s.copyWith(observationMinMinutes: v),
          ),
        ),
        _NumberTile(
          label: 'Quiet time after arr activity (minutes)',
          help: 'Keep waiting while Radarr or Sonarr is retrying. Escalation '
              'starts only after no new recovery activity for this long.',
          value: s.observationQuietMinutes,
          onChanged: (v) => setState(
            () => _edited = s.copyWith(observationQuietMinutes: v),
          ),
        ),
        _NumberTile(
          label: 'Recovery settle time (minutes)',
          help: 'When the failed queue item clears, allow imports and library '
              'state this long to settle before deciding the outcome.',
          value: s.observationSettleMinutes,
          onChanged: (v) => setState(
            () => _edited = s.copyWith(observationSettleMinutes: v),
          ),
        ),
        const _SectionLabel('Shared AI'),
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 4, 16, 8),
          child: Text(
            'Uses the shared ${_providerOption(_credentials!)?.label ?? _credentials!.ai.provider} '
            'provider and credential from Admin > Providers & Credentials. '
            'The assistant model there is ${_credentials!.ai.model}. You can '
            'choose a different model below for remediation only.',
            style: const TextStyle(
              color: AppTheme.textSecondary,
              fontSize: 13,
              height: 1.35,
            ),
          ),
        ),
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 4, 16, 8),
          child: DropdownButtonFormField<String>(
            key: ValueKey(
              'remediation-model-${_credentials!.ai.provider}-$_modelSelection',
            ),
            initialValue: _modelSelection,
            isExpanded: true,
            decoration: const InputDecoration(
              labelText: 'Remediation model',
              isDense: true,
            ),
            items: [
              DropdownMenuItem(
                value: _sharedModel,
                child: Text('Use shared model (${_credentials!.ai.model})'),
              ),
              ...?_providerOption(_credentials!)?.models.map(
                    (model) => DropdownMenuItem(
                      value: model.id,
                      child: Text(model.label),
                    ),
                  ),
              const DropdownMenuItem(
                value: _customModel,
                child: Text('Custom model ID'),
              ),
            ],
            onChanged: (value) {
              if (value != null) {
                setState(() => _modelSelection = value);
              }
            },
          ),
        ),
        if (_modelSelection == _customModel)
          Padding(
            padding: const EdgeInsets.fromLTRB(16, 4, 16, 8),
            child: TextField(
              controller: _customModelController,
              decoration: const InputDecoration(
                labelText: 'Custom model ID',
                helperText:
                    'Saving runs a small response test before activation.',
                isDense: true,
              ),
            ),
          ),
        const Padding(
          padding: EdgeInsets.fromLTRB(16, 0, 16, 8),
          child: Text(
            'If the shared provider changes later, Cantinarr falls back to its '
            'shared model until a remediation override is tested for the new provider.',
            style: TextStyle(
              color: AppTheme.textSecondary,
              fontSize: 12,
              height: 1.35,
            ),
          ),
        ),
        const _SectionLabel('Limits'),
        _NumberTile(
          label: 'Max steps per run',
          value: s.maxSteps,
          onChanged: (v) => setState(() => _edited = s.copyWith(maxSteps: v)),
        ),
        _NumberTile(
          label: 'Max output tokens per turn',
          help: 'API-key providers receive this as a request cap. For shared '
              'OpenAI OAuth, Cantinarr watches Codex usage reports and interrupts '
              'the turn when reported output reaches the limit. Reports are '
              'best-effort rather than a request-side hard cap and can arrive '
              'after the model has already exceeded the boundary.',
          value: s.maxTurnTokens,
          onChanged: (v) =>
              setState(() => _edited = s.copyWith(maxTurnTokens: v)),
        ),
        _NumberTile(
          label: 'Max wall-clock (seconds)',
          value: s.maxWallClockSecs,
          onChanged: (v) =>
              setState(() => _edited = s.copyWith(maxWallClockSecs: v)),
        ),
        _NumberTile(
          label: 'Daily run cap',
          value: s.dailyRunCap,
          onChanged: (v) =>
              setState(() => _edited = s.copyWith(dailyRunCap: v)),
        ),
        _NumberTile(
          label: 'Wait for a user reply (hours)',
          value: s.maxUserWaitHours,
          onChanged: (v) =>
              setState(() => _edited = s.copyWith(maxUserWaitHours: v)),
        ),
        _NumberTile(
          label: 'Failed auto investigations before pausing',
          value: s.circuitBreakerGiveups,
          onChanged: (v) =>
              setState(() => _edited = s.copyWith(circuitBreakerGiveups: v)),
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
                  : const Text('Save'),
            ),
          ),
        ),
      ],
    );
  }
}

/// A labelled integer field. Empty input is treated as 0.
class _NumberTile extends StatefulWidget {
  final String label;
  final String? help;
  final int value;
  final ValueChanged<int> onChanged;

  const _NumberTile({
    required this.label,
    this.help,
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
  void didUpdateWidget(covariant _NumberTile oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.value != widget.value &&
        int.tryParse(_controller.text) != widget.value) {
      _controller.text = widget.value.toString();
    }
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
      subtitle: widget.help == null
          ? null
          : Text(
              widget.help!,
              style: const TextStyle(
                color: AppTheme.textSecondary,
                fontSize: 12,
                height: 1.3,
              ),
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
