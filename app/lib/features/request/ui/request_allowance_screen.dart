import 'package:cantinarr/features/discover/ui/browse_labels.dart';
import 'package:cantinarr/l10n/l10n.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/layout/adaptive.dart';
import '../../../core/theme/app_theme.dart';
import '../data/request_quota.dart';
import '../logic/request_quota_provider.dart';

String allowanceDate(BuildContext context, DateTime value) {
  final local = value.toLocal();
  final l = MaterialLocalizations.of(context);
  return context.l10n.requestDateAtTime(
      l.formatMediumDate(local),
      l.formatTimeOfDay(TimeOfDay.fromDateTime(local),
          alwaysUse24HourFormat: MediaQuery.alwaysUse24HourFormatOf(context)));
}

class RequestAllowanceScreen extends ConsumerWidget {
  const RequestAllowanceScreen({super.key});
  @override
  Widget build(BuildContext context, WidgetRef ref) => Scaffold(
        appBar: AppBar(title: Text(context.l10n.browseRequestAllowance)),
        body: const CenteredContent(
            child: SingleChildScrollView(
          padding: EdgeInsets.all(16),
          child: RequestAllowanceSection(),
        )),
      );
}

/// Embedded in Request Defaults and each user's existing request settings.
/// Editing a complete rule has its own explicit Save action.
class RequestAllowanceSection extends ConsumerWidget {
  final bool editDefaults;
  final int? userId;
  final String? username;
  const RequestAllowanceSection(
      {super.key, this.editDefaults = false, this.userId, this.username});

  bool get editable => editDefaults || userId != null;
  String get path => editDefaults
      ? '/api/admin/request-quotas'
      : userId != null
          ? '/api/admin/users/$userId/request-quotas'
          : '/api/me/request-quotas';

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    if (!ref.watch(requestQuotasSupportedProvider))
      return const SizedBox.shrink();
    final data = ref.watch(requestQuotaViewProvider(path));
    return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
      Text(
          editable
              ? context.l10n.browseRequestAllowances
              : context.l10n.browseYourAllowance,
          style: Theme.of(context).textTheme.titleMedium),
      const SizedBox(height: 8),
      Text(
          editDefaults
              ? context.l10n
                  .browseDefaultsForUserAndKidsAccountsAdminsAreExemptEachAllowanceIsSharedAcrossLibraries
              : context.l10n
                  .browseRollingWindowsCountAcceptedRequestsIncludingRequestsAwaitingApprovalTVCountsSeasonsBooksCountEachFor,
          style: Theme.of(context).textTheme.bodySmall),
      const SizedBox(height: 8),
      data.when(
        loading: () => const LinearProgressIndicator(),
        error: (e, s) => Row(children: [
          Expanded(
              child:
                  Text(context.l10n.browseRequestAllowancesCouldNotBeLoaded)),
          TextButton(
              onPressed: () => ref.invalidate(requestQuotaViewProvider(path)),
              child: Text(context.l10n.browseRetry)),
        ]),
        data: (view) =>
            Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
          if (view.exempt)
            Padding(
                padding: EdgeInsets.symmetric(vertical: 8),
                child: Text(context.l10n
                    .browseAdminsHaveUnlimitedRequestsTheseRulesApplyIfTheAccountBecomesAUser)),
          for (final a in view.allowances)
            ListTile(
              contentPadding: EdgeInsets.zero,
              title: Text(browseLabel(context, a.label)),
              subtitle: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(userId == null
                        ? localizedAllowanceRule(context, a.count, a.windowDays)
                        : a.source == 'default'
                            ? context.l10n.requestInheritedRule(
                                localizedAllowanceRule(
                                    context, a.count, a.windowDays))
                            : context.l10n.requestOverrideRule(
                                localizedAllowanceRule(
                                    context, a.count, a.windowDays))),
                    if (!editDefaults) ...[
                      Text(view.exempt
                          ? context.l10n.requestAdminExemptUsage(a.used)
                          : a.count == null
                              ? context.l10n
                                  .requestRecentUsage(a.used, a.windowDays)
                              : context.l10n.requestRemainingUsage(
                                  a.used, a.remaining ?? 0)),
                      if (a.nextReplenishesAt != null &&
                          !view.exempt &&
                          a.count != null)
                        Text(context.l10n.requestNextReturns(
                            allowanceDate(context, a.nextReplenishesAt!))),
                      if (a.fullyReplenishesAt != null &&
                          !view.exempt &&
                          a.count != null)
                        Text(context.l10n.requestFullyReplenishes(
                            allowanceDate(context, a.fullyReplenishesAt!))),
                      if (a.used == 0 && a.count != null && !view.exempt)
                        Text(context.l10n.browseFullyReplenished),
                    ],
                  ]),
              trailing: editable
                  ? IconButton(
                      tooltip: context.l10n
                          .requestEditAllowance(browseLabel(context, a.label)),
                      icon: const Icon(Icons.edit_outlined),
                      onPressed: () => _edit(context, ref, a))
                  : null,
            ),
          if (userId != null && !view.exempt)
            TextButton.icon(
              icon: const Icon(Icons.restart_alt),
              label: Text(context.l10n.browseResetSelectedAllowances),
              onPressed: view.allowances.any((a) => a.used > 0)
                  ? () => _reset(context, ref, view)
                  : null,
            ),
        ]),
      ),
    ]);
  }

  Future<void> _edit(
      BuildContext context, WidgetRef ref, RequestAllowance a) async {
    final saved = await showDialog<bool>(
        context: context,
        builder: (_) => _AllowanceRuleDialog(
            allowance: a, path: path, userOverride: userId != null));
    if (saved == true) ref.invalidate(requestQuotaViewProvider(path));
  }

  Future<void> _reset(
      BuildContext context, WidgetRef ref, RequestQuotaView view) async {
    final selected = <String>{};
    final available = view.allowances.where((a) => a.used > 0).toList();
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (dialogContext) => StatefulBuilder(
          builder: (context, setState) => AlertDialog(
                title: Text(context.l10n.requestResetUserAllowance(
                    username ?? context.l10n.requestThisUser)),
                content: SingleChildScrollView(
                    child: Column(
                        mainAxisSize: MainAxisSize.min,
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                      Text(context.l10n
                          .browseChooseTheUsageToClearThisResetIsRecordedInTheAdministratorAudit),
                      for (final a in available)
                        CheckboxListTile(
                          contentPadding: EdgeInsets.zero,
                          value: selected.contains(a.id),
                          title: Text(browseLabel(context, a.label)),
                          subtitle: Text(a.count == null
                              ? context.l10n.requestClearUnlimitedUsage(a.used)
                              : context.l10n.requestClearLimitedUsage(a.used,
                                  a.count! - (a.remaining ?? 0), a.count!)),
                          onChanged: (v) => setState(() {
                            if (v == true) {
                              selected.add(a.id);
                            } else {
                              selected.remove(a.id);
                            }
                          }),
                        ),
                    ])),
                actions: [
                  TextButton(
                      onPressed: () => Navigator.pop(dialogContext, false),
                      child: Text(context.l10n.browseCancel)),
                  FilledButton(
                      onPressed: selected.isEmpty
                          ? null
                          : () => Navigator.pop(dialogContext, true),
                      child: Text(context.l10n.browseResetSelected)),
                ],
              )),
    );
    if (confirmed != true || !context.mounted) return;
    try {
      await ref.read(requestQuotaServiceProvider).reset(
          userId!, available.where((a) => selected.contains(a.id)).toList());
      ref.invalidate(requestQuotaViewProvider(path));
      if (context.mounted)
        ScaffoldMessenger.of(context).showSnackBar(SnackBar(
            content: Text(context.l10n.browseSelectedAllowancesReset)));
    } catch (e) {
      if (context.mounted)
        ScaffoldMessenger.of(context)
            .showSnackBar(SnackBar(content: Text(_allowanceError(context, e))));
    }
  }
}

String _allowanceError(BuildContext context, Object e) {
  if (e is DioException &&
      e.response?.data is Map &&
      e.response!.data['error'] is String) {
    return e.response!.data['error'] as String;
  }
  return context.l10n.browseCouldNotSaveTheAllowancePleaseRetry;
}

class _AllowanceRuleDialog extends ConsumerStatefulWidget {
  final RequestAllowance allowance;
  final String path;
  final bool userOverride;
  const _AllowanceRuleDialog(
      {required this.allowance,
      required this.path,
      required this.userOverride});
  @override
  ConsumerState<_AllowanceRuleDialog> createState() =>
      _AllowanceRuleDialogState();
}

class _AllowanceRuleDialogState extends ConsumerState<_AllowanceRuleDialog> {
  late final TextEditingController _count;
  late String _mode;
  late int _days;
  bool _saving = false;
  String? _error;
  @override
  void initState() {
    super.initState();
    _count = TextEditingController(text: '${widget.allowance.count ?? 5}');
    _days = widget.allowance.windowDays;
    _mode = widget.userOverride && widget.allowance.source == 'default'
        ? 'inherit'
        : widget.allowance.count == null
            ? 'unlimited'
            : 'limited';
  }

  @override
  void dispose() {
    _count.dispose();
    super.dispose();
  }

  Future<void> _save() async {
    final count = int.tryParse(_count.text.trim());
    if (_mode == 'limited' && (count == null || count < 0 || count > 1000000)) {
      setState(
          () => _error = context.l10n.browseEnterAWholeNumberFrom0To1000000);
      return;
    }
    setState(() {
      _saving = true;
      _error = null;
    });
    try {
      await ref.read(requestQuotaServiceProvider).save(widget.path, {
        ...widget.allowance.keyJson,
        'inherit': _mode == 'inherit',
        'count': _mode == 'limited' ? count : null,
        'window_days': _days,
      });
      if (mounted) Navigator.pop(context, true);
    } catch (e) {
      if (mounted)
        setState(() {
          _saving = false;
          _error = _allowanceError(context, e);
        });
    }
  }

  @override
  Widget build(BuildContext context) => AlertDialog(
        title: Text(context.l10n.requestAllowanceForType(
            browseLabel(context, widget.allowance.label))),
        content: SingleChildScrollView(
            child: Column(mainAxisSize: MainAxisSize.min, children: [
          DropdownButtonFormField<String>(
            initialValue: _mode,
            decoration: InputDecoration(labelText: context.l10n.browseRule),
            items: [
              if (widget.userOverride)
                DropdownMenuItem(
                    value: 'inherit',
                    child: Text(context.l10n.browseInheritUserDefault)),
              DropdownMenuItem(
                  value: 'unlimited',
                  child: Text(context.l10n.browseUnlimited)),
              DropdownMenuItem(
                  value: 'limited',
                  child: Text(context.l10n.browseSetAllowance)),
            ],
            onChanged: _saving ? null : (v) => setState(() => _mode = v!),
          ),
          if (_mode == 'limited') ...[
            const SizedBox(height: 12),
            TextField(
                controller: _count,
                enabled: !_saving,
                keyboardType: TextInputType.number,
                decoration: InputDecoration(
                    labelText: context.l10n.browseUnits,
                    helperText: context.l10n.browseZeroAllowsNoNewUnits)),
            const SizedBox(height: 12),
            DropdownButtonFormField<int>(
              initialValue: _days,
              decoration:
                  InputDecoration(labelText: context.l10n.browseRollingWindow),
              items: [
                for (final d in [1, 7, 30])
                  DropdownMenuItem(
                      value: d, child: Text(context.l10n.requestWindowDays(d)))
              ],
              onChanged: _saving ? null : (v) => setState(() => _days = v!),
            ),
          ],
          if (_error != null)
            Padding(
                padding: const EdgeInsets.only(top: 12),
                child: Text(browseLabel(context, _error!),
                    style: const TextStyle(color: AppTheme.error))),
        ])),
        actions: [
          TextButton(
              onPressed: _saving ? null : () => Navigator.pop(context),
              child: Text(context.l10n.browseCancel)),
          FilledButton(
              onPressed: _saving ? null : _save,
              child: Text(_saving
                  ? context.l10n.browseSaving
                  : context.l10n.browseSaveAllowance)),
        ],
      );
}
