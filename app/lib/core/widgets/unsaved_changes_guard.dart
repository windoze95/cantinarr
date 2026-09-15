import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../platform/unsaved_changes_stub.dart'
    if (dart.library.js_interop) '../platform/unsaved_changes_web.dart';

/// A copy of the last loaded/saved values, including mutable maps and lists.
/// Compare values, not "touched" flags, so reverting an edit is clean again.
class SettingsDraft {
  String? _saved;

  void markSaved(Object? values) => _saved = jsonEncode(values);

  bool hasChanges(Object? values) =>
      _saved != null && _saved != jsonEncode(values);
}

final unsavedChangesProvider = Provider<UnsavedChangesRegistry>((ref) {
  final registry = UnsavedChangesRegistry();
  final removeUnloadWarning = registerUnsavedChangesWarning(
    () => registry._editors.any((editor) => editor.widget.hasChanges()),
  );
  ref.onDispose(removeUnloadWarning);
  return registry;
});

/// Route exits cover app/sidebar navigation, browser history, and native Back.
/// Pushing a child page preserves the editor, so it does not discard its draft.
class UnsavedChangesRegistry {
  final _editors = <_UnsavedChangesGuardState>{};
  Future<bool>? _confirmation;

  Future<bool> confirmExit(BuildContext context, GoRouterState route) async {
    final dirty = _editors.any((editor) =>
        editor._pageKey == route.pageKey && editor.widget.hasChanges());
    if (!dirty) return true;
    if (_confirmation != null) return _confirmation!;
    try {
      return await (_confirmation = confirmDiscardChanges(context));
    } finally {
      _confirmation = null;
    }
  }
}

Future<bool> confirmDiscardChanges(BuildContext context) async =>
    await showDialog<bool>(
      context: context,
      builder: (dialogContext) => AlertDialog(
        title: const Text('Discard unsaved changes?'),
        content: const Text(
          'You have changes that haven’t been saved. If you leave, they will be lost.',
        ),
        actions: [
          TextButton(
            autofocus: true,
            onPressed: () => Navigator.of(dialogContext).pop(false),
            child: const Text('Keep editing'),
          ),
          TextButton(
            onPressed: () => Navigator.of(dialogContext).pop(true),
            style: TextButton.styleFrom(
              foregroundColor: Theme.of(dialogContext).colorScheme.error,
            ),
            child: const Text('Discard changes'),
          ),
        ],
      ),
    ) ==
    true;

/// Reads the live draft on exit, including text typed since the last rebuild.
/// Register each independently saved editor; only the exiting page is checked.
class UnsavedChangesGuard extends ConsumerStatefulWidget {
  const UnsavedChangesGuard({
    super.key,
    required this.hasChanges,
    this.isSaving = false,
    this.isDialog = false,
    required this.child,
  });

  final bool Function() hasChanges;
  final bool isSaving;
  final bool isDialog;
  final Widget child;

  @override
  ConsumerState<UnsavedChangesGuard> createState() =>
      _UnsavedChangesGuardState();
}

class _UnsavedChangesGuardState extends ConsumerState<UnsavedChangesGuard> {
  late final _registry = ref.read(unsavedChangesProvider);
  ValueKey<String>? _pageKey;
  bool _confirmingDialog = false;

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    final router = GoRouter.maybeOf(context);
    if (router != null) {
      if (widget.isDialog) {
        _pageKey ??= router.routerDelegate.currentConfiguration.last.pageKey;
      } else {
        _pageKey = GoRouterState.of(context).pageKey;
      }
    }
    _registry._editors.add(this);
  }

  @override
  void dispose() {
    _registry._editors.remove(this);
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final child = ExcludeFocus(
      excluding: widget.isSaving,
      child: AbsorbPointer(
        absorbing: widget.isSaving,
        child: widget.child,
      ),
    );
    if (!widget.isDialog) return child;
    return PopScope<Object?>(
      canPop: false,
      onPopInvokedWithResult: (didPop, result) async {
        if (didPop || _confirmingDialog || widget.isSaving) return;
        _confirmingDialog = true;
        final discard =
            !widget.hasChanges() || await confirmDiscardChanges(context);
        _confirmingDialog = false;
        if (discard && context.mounted) Navigator.of(context).pop(result);
      },
      child: child,
    );
  }
}
