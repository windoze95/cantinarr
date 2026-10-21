import 'package:flutter/foundation.dart';

import '../../../core/models/backend_connection.dart';
import '../../auth/data/auth_service.dart';
import 'media_access_service.dart';

/// An explicit administrator choice. A matching name never selects a user.
class MediaImportChoice {
  RemoteMediaServerUser remote;
  bool listed = true;
  bool selected = false;
  int? targetUserId;
  bool requiresMapping = false;
  bool uncertain = false;
  MediaServerAccountRow? account;
  MediaServerImportResult? result;

  MediaImportChoice(this.remote);

  String get username =>
      remote.name.trim().isEmpty ? remote.id.trim() : remote.name.trim();
  bool get complete => result?.linked == true;
  bool get alreadyLinked =>
      account != null &&
      (result == null ||
          (result?.userId != null && account!.userId != result!.userId));
  bool get selectable =>
      !alreadyLinked &&
      listed &&
      !complete &&
      !uncertain &&
      (!requiresMapping || targetUserId != null);
}

/// Owns submission receipts independently of the account list. In particular,
/// retrying a link never recreates a user or replaces their invitation token.
class MediaUserImportController extends ChangeNotifier {
  static const maxSelection = 200;
  final MediaAccessService service;
  final Future<List<UserSummary>> Function() readUsers;
  final ServiceInstance server;
  final String serverUrl;
  final bool supportsExplicitLinking;
  final bool supportsManagement;

  List<UserSummary> users = [];
  List<MediaImportChoice> choices = [];
  bool loading = true;
  bool busy = false;
  bool manageAccess = false;
  bool submitted = false;
  String? error;
  bool _disposed = false;

  MediaUserImportController({
    required this.service,
    required this.readUsers,
    required this.server,
    required this.serverUrl,
    required this.supportsExplicitLinking,
    required this.supportsManagement,
  });

  void _changed() {
    if (!_disposed) notifyListeners();
  }

  @override
  void dispose() {
    _disposed = true;
    super.dispose();
  }

  List<MediaImportChoice> get selected =>
      choices.where((c) => c.selected).toList();
  List<MediaImportChoice> get pending =>
      selected.where((c) => c.selectable).toList();
  int get selectedIncompleteCount => selected.where((c) => !c.complete).length;
  List<int> get successfulUserIds => selected
      .where((c) => c.complete && c.result?.userId != null)
      .map((c) => c.result!.userId!)
      .toSet()
      .toList();
  bool get canSubmit =>
      supportsExplicitLinking &&
      !loading &&
      !busy &&
      error == null &&
      pending.isNotEmpty &&
      selectedIncompleteCount <= maxSelection;

  String ownerName(int userId) =>
      users.where((u) => u.id == userId).map((u) => u.username).firstOrNull ??
      'User $userId';

  Future<void> load() async {
    loading = true;
    error = null;
    _changed();
    try {
      final values = await Future.wait<Object>([
        service.listRemoteUsers(server.id),
        readUsers(),
        service.listAccounts(),
      ]);
      users = values[1] as List<UserSummary>;
      final accounts = values[2] as List<MediaServerAccountRow>;
      final previous = {for (final c in choices) c.remote.id: c};
      choices = (values[0] as List<RemoteMediaServerUser>).map((remote) {
        final choice = previous[remote.id] ?? MediaImportChoice(remote);
        choice.remote = remote;
        choice.listed = true;
        choice.account = _accountFor(accounts, remote.id);
        choice.requiresMapping =
            users.any((u) => u.username == choice.username);
        return choice;
      }).toList();
      final listedIds = choices.map((c) => c.remote.id).toSet();
      // A refresh may no longer list an imported account, but its receipt and
      // invitation still belong to this session and must remain available.
      for (final choice in previous.values) {
        if (!listedIds.contains(choice.remote.id) &&
            (choice.result != null || choice.uncertain)) {
          choice.listed = false;
          choice.account = _accountFor(accounts, choice.remote.id);
          choices.add(choice);
        }
      }
    } catch (_) {
      error = 'Could not load accounts and their Cantinarr links. '
          'Refresh before importing; existing links are unknown.';
    }
    loading = false;
    _changed();
  }

  MediaServerAccountRow? _accountFor(
          List<MediaServerAccountRow> accounts, String remoteId) =>
      accounts
          .where((a) => a.instanceId == server.id && a.remoteUserId == remoteId)
          .firstOrNull;

  void select(MediaImportChoice choice, bool value) {
    if (busy || !choice.selectable) return;
    if (value && selectedIncompleteCount >= maxSelection) return;
    choice.selected = value;
    _changed();
  }

  void selectAll(Iterable<MediaImportChoice> visible) {
    if (busy) return;
    for (final choice in visible) {
      if (selectedIncompleteCount >= maxSelection) break;
      if (choice.selectable && !choice.remote.isAdministrator) {
        choice.selected = true;
      }
    }
    _changed();
  }

  void clearSelection() {
    if (busy) return;
    for (final choice in choices) {
      if (!choice.complete) choice.selected = false;
    }
    _changed();
  }

  void mapTo(MediaImportChoice choice, int? userId) {
    if (busy ||
        choice.complete ||
        choice.alreadyLinked ||
        choice.uncertain ||
        choice.result?.created == true) {
      return;
    }
    choice.targetUserId = userId;
    if (!choice.selectable) choice.selected = false;
    _changed();
  }

  void setManagement(bool value) {
    if (busy || submitted) return;
    manageAccess = value;
    _changed();
  }

  Future<void> submit() async {
    if (!canSubmit) return;
    busy = true;
    submitted = true;
    _changed();
    final batch = pending;
    final creates = batch
        .where((c) => c.targetUserId == null && c.result?.userId == null)
        .toList();
    final links = batch.where((c) => !creates.contains(c)).toList();
    if (creates.isNotEmpty) {
      try {
        final results = await service.importAccounts(
          instanceId: server.id,
          remoteUserIds: creates.map((c) => c.remote.id).toList(),
          serverUrl: serverUrl,
          manageAccess: supportsManagement ? manageAccess : false,
        );
        for (final choice in creates) {
          final result = results
              .where((r) => r.remoteUserId == choice.remote.id)
              .firstOrNull;
          if (result == null) {
            choice.uncertain = true;
          } else {
            choice.result = result;
            if (result.userId != null) choice.targetUserId = result.userId;
          }
        }
      } catch (_) {
        for (final choice in creates) {
          choice.uncertain = true;
        }
      }
      _changed();
    }
    for (final choice in links) {
      final userId = choice.result?.userId ?? choice.targetUserId!;
      try {
        final account = await service.link(
          userId: userId,
          instanceId: server.id,
          remoteUserId: choice.remote.id,
          manageAccess: supportsManagement ? manageAccess : false,
        );
        final previous = choice.result;
        choice.account = account;
        choice.result = MediaServerImportResult(
          remoteUserId: choice.remote.id,
          remoteUsername: choice.remote.name,
          userId: userId,
          username: previous?.username ?? ownerName(userId),
          created: previous?.created ?? false,
          linked: account.granted,
          link: previous?.link ?? '',
          originSource: previous?.originSource ?? '',
          plexIdentityError: account.plexIdentityError,
          error: account.granted ? '' : 'link_failed',
        );
      } catch (_) {
        choice.uncertain = true;
      }
      _changed();
    }
    // Always refresh mappings, including username conflicts and links won by
    // another administrator. This read never causes an implicit name mapping.
    await reconcile();
    busy = false;
    _changed();
  }

  Future<void> reconcile() async {
    error = null;
    try {
      final values =
          await Future.wait<Object>([readUsers(), service.listAccounts()]);
      users = values[0] as List<UserSummary>;
      final accounts = values[1] as List<MediaServerAccountRow>;
      for (final choice in choices) {
        final account = _accountFor(accounts, choice.remote.id);
        choice.account = account;
        choice.requiresMapping =
            users.any((u) => u.username == choice.username);
        if (!choice.selected || choice.complete) continue;
        if (account != null) {
          final expected = choice.result?.userId ?? choice.targetUserId;
          if (expected != null && account.userId != expected) {
            final previous = choice.result;
            choice.result = MediaServerImportResult(
              remoteUserId: choice.remote.id,
              remoteUsername: choice.remote.name,
              userId: previous?.userId ?? expected,
              username: previous?.username ?? ownerName(expected),
              created: previous?.created ?? false,
              link: previous?.link ?? '',
              originSource: previous?.originSource ?? '',
              error: 'already_linked',
            );
            choice.selected = false;
          } else {
            final previous = choice.result;
            choice.targetUserId = account.userId;
            choice.result = MediaServerImportResult(
              remoteUserId: choice.remote.id,
              remoteUsername: choice.remote.name,
              userId: account.userId,
              username: ownerName(account.userId),
              created: previous?.created ?? false,
              linked: account.granted,
              link: previous?.link ?? '',
              originSource: previous?.originSource ?? '',
              plexIdentityError: account.plexIdentityError,
              error: account.granted ? '' : 'link_failed',
            );
          }
        }
        choice.uncertain = false;
        if (!choice.selectable && !choice.complete) choice.selected = false;
      }
    } catch (_) {
      error =
          'Could not verify the latest accounts. Completed results are kept. '
          'Check current state before retrying incomplete accounts.';
    }
    _changed();
  }
}
