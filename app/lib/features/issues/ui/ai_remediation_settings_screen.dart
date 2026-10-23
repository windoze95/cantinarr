import 'package:cantinarr/l10n/operations_labels.dart';
import 'package:cantinarr/l10n/l10n.dart';
import 'package:go_router/go_router.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/layout/adaptive.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/settings_highlight.dart';
import '../../../core/widgets/unsaved_changes_guard.dart';
import '../../settings/data/credentials_service.dart';
import '../../settings/settings_anchors.dart';
import '../data/issue_models.dart';
import '../logic/issues_provider.dart';

/// Admin screen for the AI-remediation settings. Clones
/// `RequestSettingsScreen`'s load → edit → Save shape: a master Enabled
/// switch, sub-toggles, a remediation-mode dropdown, and numeric bound fields.
class AiRemediationSettingsScreen extends ConsumerStatefulWidget {
  /// Settings-search anchor to scroll to and flash on arrival.
  final String? highlightId;

  const AiRemediationSettingsScreen({super.key, this.highlightId});

  @override
  ConsumerState<AiRemediationSettingsScreen> createState() =>
      _AiRemediationSettingsScreenState();
}

class _AiRemediationSettingsScreenState
    extends ConsumerState<AiRemediationSettingsScreen> {
  final _draft = SettingsDraft();
  Object get _draftValues => [
        _edited?.toJson(),
        _modelSelection,
        if (_modelSelection == _customModel) _customModelController.text,
      ];
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
    return m != null ? m.group(1)! : context.l10n.opsSomethingWentWrongMessage;
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
        _draft.markSaved(_draftValues);
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
        SnackBar(content: Text(context.l10n.opsEnterACustomModelID)),
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
        _draft.markSaved(_draftValues);
        _saving = false;
      });
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(context.l10n.opsSaved)),
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
  Widget build(BuildContext context) => UnsavedChangesGuard(
        hasChanges: () => _draft.hasChanges(_draftValues),
        isSaving: _saving,
        child: _buildPage(context),
      );

  Widget _buildPage(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: Text(context.l10n.opsAIRemediation)),
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
                                _friendlyError(_error ??
                                    context.l10n.opsSomethingWentWrongMessage),
                                style: const TextStyle(color: AppTheme.error),
                                textAlign: TextAlign.center),
                            const SizedBox(height: 12),
                            ElevatedButton(
                                onPressed: _load,
                                child: Text(context.l10n.opsRetry)),
                          ],
                        ),
                      ),
                    )
                  : _buildBody(_edited!)),
    );
  }

  /// Wraps [child] with the settings-search highlight for [anchorId].
  Widget _anchor(String anchorId, Widget child) => SettingsHighlight(
        anchorId: anchorId,
        highlightId: widget.highlightId,
        child: child,
      );

  Widget _buildBody(RemediationSettings s) {
    return ListView(
      // Build every child while a settings-search highlight needs to find
      // its anchor (see SettingsHighlight).
      cacheExtent: SettingsHighlight.cacheExtentFor(widget.highlightId),
      padding: const EdgeInsets.symmetric(vertical: 8),
      children: [
        Padding(
          padding: EdgeInsets.fromLTRB(16, 8, 16, 4),
          child: Text(
            context.l10n.opsLetTheAssistantInvestigateReportedProblemsAnd,
            style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
          ),
        ),
        // The pipeline's other two surfaces, one tap away — the queue, its
        // governing rules, and this config are one system, not three
        // destinations an admin must already know about.
        _anchor(
          SettingsAnchors.remediationRules,
          ListTile(
            leading:
                const Icon(Icons.rule_folder_outlined, color: AppTheme.accent),
            title: Text(context.l10n.opsStandingAutoApprovals,
                style: TextStyle(color: AppTheme.textPrimary)),
            subtitle: Text(context.l10n.opsTheFixesApprovedWithoutPagingYou,
                style: TextStyle(color: AppTheme.textSecondary, fontSize: 12)),
            trailing: const Icon(Icons.chevron_right,
                color: AppTheme.textSecondary, size: 18),
            onTap: () => context.push('/settings/agent-approval-rules'),
          ),
        ),
        _anchor(
          SettingsAnchors.remediationFixes,
          ListTile(
            leading:
                const Icon(Icons.build_circle_outlined, color: AppTheme.accent),
            title: Text(context.l10n.opsAgentFixes,
                style: TextStyle(color: AppTheme.textPrimary)),
            subtitle: Text(
                context.l10n.opsAwaitingReviewAndEverythingAlreadyDone,
                style: TextStyle(color: AppTheme.textSecondary, fontSize: 12)),
            trailing: const Icon(Icons.chevron_right,
                color: AppTheme.textSecondary, size: 18),
            onTap: () => context.push('/agent-actions'),
          ),
        ),
        _SectionLabel(context.l10n.opsGeneral),
        _anchor(
          SettingsAnchors.remediationEnabled,
          SwitchListTile(
            value: s.enabled,
            onChanged: (v) => setState(() => _edited = s.copyWith(enabled: v)),
            title: Text(
              context.l10n.opsEnabled,
              style: TextStyle(
                  color: AppTheme.textPrimary, fontWeight: FontWeight.w500),
            ),
            subtitle: Text(
              context.l10n.opsMasterSwitchForTheRemediationAssistant,
              style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
            ),
          ),
        ),
        _anchor(
          SettingsAnchors.remediationAutoDispatch,
          SwitchListTile(
            value: s.autoDispatch,
            onChanged: (v) =>
                setState(() => _edited = s.copyWith(autoDispatch: v)),
            title: Text(
              context.l10n.opsAutoDispatchOnDetectedProblems,
              style: TextStyle(
                  color: AppTheme.textPrimary, fontWeight: FontWeight.w500),
            ),
            subtitle: Text(
              context.l10n.opsTrackDetectedProblemsQuietlyWhileRadarrOr,
              style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
            ),
          ),
        ),
        _anchor(
          SettingsAnchors.remediationAllowReporting,
          SwitchListTile(
            value: s.allowReporting,
            onChanged: (v) =>
                setState(() => _edited = s.copyWith(allowReporting: v)),
            title: Text(
              context.l10n.opsAllowProblemReporting,
              style: TextStyle(
                  color: AppTheme.textPrimary, fontWeight: FontWeight.w500),
            ),
            subtitle: Text(
              context.l10n.opsShowAReportAProblemButtonTo,
              style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
            ),
          ),
        ),
        _anchor(
          SettingsAnchors.remediationMarkResolvedRead,
          SwitchListTile(
            value: s.markResolvedAsRead,
            onChanged: (v) =>
                setState(() => _edited = s.copyWith(markResolvedAsRead: v)),
            title: Text(
              context.l10n.opsMarkResolvedIssuesAsRead,
              style: TextStyle(
                  color: AppTheme.textPrimary, fontWeight: FontWeight.w500),
            ),
            subtitle: Text(
              context.l10n.opsClearTheUnreadDotWhenAnIssue,
              style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
            ),
          ),
        ),
        _anchor(
          SettingsAnchors.remediationMode,
          ListTile(
            title: Text(
              context.l10n.opsMode,
              style: TextStyle(
                  color: AppTheme.textPrimary, fontWeight: FontWeight.w500),
            ),
            subtitle: Text(
              context.l10n.opsWhetherTheAssistantMayPrepareAFix,
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
                    child: Text(localizeOperationsLabel(context, a.label)),
                  ),
              ],
              onChanged: (v) {
                if (v == null) return;
                setState(() => _edited = s.copyWith(mode: v));
              },
            ),
          ),
        ),
        _SectionLabel(context.l10n.opsAutomaticRecovery),
        Padding(
          padding: EdgeInsets.fromLTRB(16, 4, 16, 8),
          child: Text(
            context.l10n.opsDetectedDownloadProblemsStaySilentWhileThe,
            style: TextStyle(
              color: AppTheme.textSecondary,
              fontSize: 13,
              height: 1.35,
            ),
          ),
        ),
        _anchor(
          SettingsAnchors.remediationWatchTime,
          _NumberTile(
            label: context.l10n.opsMinimumWatchTimeMinutes,
            help: context.l10n.opsWaitAtLeastThisLongBeforeAny,
            value: s.observationMinMinutes,
            onChanged: (v) => setState(
              () => _edited = s.copyWith(observationMinMinutes: v),
            ),
          ),
        ),
        _anchor(
          SettingsAnchors.remediationQuietTime,
          _NumberTile(
            label: context.l10n.opsQuietTimeAfterArrActivityMinutes,
            help: context.l10n.opsKeepWaitingWhileRadarrOrSonarrIs,
            value: s.observationQuietMinutes,
            onChanged: (v) => setState(
              () => _edited = s.copyWith(observationQuietMinutes: v),
            ),
          ),
        ),
        _anchor(
          SettingsAnchors.remediationSettleTime,
          _NumberTile(
            label: context.l10n.opsRecoverySettleTimeMinutes,
            help: context.l10n.opsWhenTheFailedQueueItemClearsAllow,
            value: s.observationSettleMinutes,
            onChanged: (v) => setState(
              () => _edited = s.copyWith(observationSettleMinutes: v),
            ),
          ),
        ),
        _SectionLabel(context.l10n.opsSharedAI),
        // The server synthesizes a default provider and model even when
        // nothing is set up, so only the live configured flag may claim a
        // provider exists.
        if (!_credentials!.ai.sharedConfigured) ...[
          Padding(
            padding: EdgeInsets.fromLTRB(16, 4, 16, 8),
            child: Text(
              context.l10n.opsNoSharedAIProviderIsSetUp,
              style: TextStyle(
                color: AppTheme.warning,
                fontSize: 13,
                height: 1.35,
              ),
            ),
          ),
          _anchor(
            SettingsAnchors.remediationModel,
            ListTile(
              leading:
                  const Icon(Icons.vpn_key_outlined, color: AppTheme.accent),
              title: Text(context.l10n.opsSetUpTheSharedProvider,
                  style: TextStyle(color: AppTheme.textPrimary)),
              subtitle: Text(context.l10n.opsAdminProvidersCredentials,
                  style:
                      TextStyle(color: AppTheme.textSecondary, fontSize: 12)),
              trailing: const Icon(Icons.chevron_right,
                  color: AppTheme.textSecondary, size: 18),
              onTap: () => context.push('/settings/credentials'),
            ),
          ),
        ] else ...[
          Padding(
            padding: const EdgeInsets.fromLTRB(16, 4, 16, 8),
            child: Text(
              context.l10n.opsSharedProviderDescription(
                  _providerOption(_credentials!)?.label ??
                      _credentials!.ai.provider,
                  _credentials!.ai.model),
              style: const TextStyle(
                color: AppTheme.textSecondary,
                fontSize: 13,
                height: 1.35,
              ),
            ),
          ),
          _anchor(
            SettingsAnchors.remediationModel,
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 4, 16, 8),
              child: DropdownButtonFormField<String>(
                key: ValueKey(
                  'remediation-model-${_credentials!.ai.provider}-$_modelSelection',
                ),
                initialValue: _modelSelection,
                isExpanded: true,
                decoration: InputDecoration(
                  labelText: context.l10n.opsRemediationModel,
                  isDense: true,
                ),
                items: [
                  DropdownMenuItem(
                    value: _sharedModel,
                    child: Text(
                        context.l10n.opsSharedModel(_credentials!.ai.model)),
                  ),
                  ...?_providerOption(_credentials!)?.models.map(
                        (model) => DropdownMenuItem(
                          value: model.id,
                          child: Text(model.label),
                        ),
                      ),
                  DropdownMenuItem(
                    value: _customModel,
                    child: Text(context.l10n.opsCustomModelID),
                  ),
                ],
                onChanged: (value) {
                  if (value != null) {
                    setState(() => _modelSelection = value);
                  }
                },
              ),
            ),
          ),
          if (_modelSelection == _customModel)
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 4, 16, 8),
              child: TextField(
                controller: _customModelController,
                decoration: InputDecoration(
                  labelText: context.l10n.opsCustomModelID,
                  helperText:
                      context.l10n.opsSavingRunsASmallResponseTestBefore,
                  isDense: true,
                ),
              ),
            ),
          Padding(
            padding: EdgeInsets.fromLTRB(16, 0, 16, 8),
            child: Text(
              context.l10n.opsIfTheSharedProviderChangesLaterCantinarr,
              style: TextStyle(
                color: AppTheme.textSecondary,
                fontSize: 12,
                height: 1.35,
              ),
            ),
          ),
        ],
        _SectionLabel(context.l10n.opsLimits),
        _anchor(
          SettingsAnchors.remediationMaxSteps,
          _NumberTile(
            label: context.l10n.opsMaxStepsPerRun,
            value: s.maxSteps,
            onChanged: (v) => setState(() => _edited = s.copyWith(maxSteps: v)),
          ),
        ),
        _anchor(
          SettingsAnchors.remediationMaxTurnTokens,
          _NumberTile(
            label: context.l10n.opsMaxOutputTokensPerTurn,
            help: context.l10n.opsAPIKeyProvidersReceiveThisAsA,
            value: s.maxTurnTokens,
            onChanged: (v) =>
                setState(() => _edited = s.copyWith(maxTurnTokens: v)),
          ),
        ),
        _anchor(
          SettingsAnchors.remediationMaxWallClock,
          _NumberTile(
            label: context.l10n.opsMaxWallClockSeconds,
            value: s.maxWallClockSecs,
            onChanged: (v) =>
                setState(() => _edited = s.copyWith(maxWallClockSecs: v)),
          ),
        ),
        _anchor(
          SettingsAnchors.remediationDailyCap,
          _NumberTile(
            label: context.l10n.opsDailyRunCap,
            value: s.dailyRunCap,
            onChanged: (v) =>
                setState(() => _edited = s.copyWith(dailyRunCap: v)),
          ),
        ),
        _anchor(
          SettingsAnchors.remediationUserWait,
          _NumberTile(
            label: context.l10n.opsWaitForAUserReplyHours,
            value: s.maxUserWaitHours,
            onChanged: (v) =>
                setState(() => _edited = s.copyWith(maxUserWaitHours: v)),
          ),
        ),
        _anchor(
          SettingsAnchors.remediationBreakerGiveups,
          _NumberTile(
            label: context.l10n.opsFailedAutoInvestigationsBeforePausing,
            value: s.circuitBreakerGiveups,
            onChanged: (v) =>
                setState(() => _edited = s.copyWith(circuitBreakerGiveups: v)),
          ),
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
                  : Text(context.l10n.opsSave),
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
