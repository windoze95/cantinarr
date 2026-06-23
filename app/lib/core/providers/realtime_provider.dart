import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../network/websocket_client.dart';

/// Raw, typed event stream from the backend WebSocket.
///
/// Watching this provider (directly or through one of the filtered family
/// providers below) lazily starts the socket: the underlying
/// [WebSocketClient] connects on first listen and then stays alive for the
/// lifetime of the app, reconnecting with exponential backoff. The stream
/// is best-effort — consumers must keep a REST polling fallback for when
/// the socket is down (server restart, older server versions).
final realtimeEventsProvider = Provider<Stream<WsEvent>>((ref) {
  final client = ref.watch(webSocketClientProvider);
  client.ensureConnected();
  return client.events;
});

/// Full queue snapshots for one download client instance
/// (`downloads_queue` events). The event `data` carries the same shape as
/// the REST queue payload (`paused`, `speed_bps`, `items`) plus
/// `instance_id`, so it can be applied directly without a REST roundtrip.
///
/// Riverpod cancels the underlying stream subscription when the last
/// listener goes away (autoDispose), so screens can't leak subscriptions.
final downloadsQueueEventsProvider =
    StreamProvider.autoDispose.family<WsEvent, String>((ref, instanceId) {
  final events = ref.watch(realtimeEventsProvider);
  return events.where((e) =>
      e.type == 'downloads_queue' && e.data['instance_id'] == instanceId);
});

/// Family key for [arrQueueChangedProvider]: which *arr instance and
/// service type ("radarr" | "sonarr") to listen for.
typedef ArrQueueKey = ({String instanceId, String serviceType});

/// Lightweight invalidation pings for one *arr instance
/// (`arr_queue_changed` events). On a ping, the consumer should refetch
/// that instance's queue via REST (debounced, in case of bursts).
final arrQueueChangedProvider =
    StreamProvider.autoDispose.family<WsEvent, ArrQueueKey>((ref, key) {
  final events = ref.watch(realtimeEventsProvider);
  return events.where((e) =>
      e.type == 'arr_queue_changed' &&
      e.data['instance_id'] == key.instanceId &&
      e.data['service_type'] == key.serviceType);
});

/// Approval decisions for the current user's own requests
/// (`request_decision` events). The backend pushes these only to the
/// requesting user, carrying `decision` ('approved'|'denied'), `title`,
/// `media_type`, and an optional `reason`. Used to surface an in-app toast.
final requestDecisionEventsProvider = StreamProvider.autoDispose<WsEvent>((ref) {
  final events = ref.watch(realtimeEventsProvider);
  return events.where((e) => e.type == 'request_decision');
});

/// Per-issue update pings (`issue_updated` events) for one issue id. On a
/// ping the thread screen refetches that issue over REST (the server emits a
/// thin ping per persisted agent step, not full bodies). Mirrors
/// [arrQueueChangedProvider].
final issueEventsProvider =
    StreamProvider.autoDispose.family<WsEvent, int>((ref, issueId) {
  final events = ref.watch(realtimeEventsProvider);
  return events.where((e) =>
      e.type == 'issue_updated' &&
      (e.data['issue_id'] as num?)?.toInt() == issueId);
});

/// New-issue / pending-approval pings (`issue_created`,
/// `agent_action_pending`). The admin issues list refreshes its contents on
/// any of these.
final issuesChangedProvider = StreamProvider.autoDispose<WsEvent>((ref) {
  final events = ref.watch(realtimeEventsProvider);
  return events.where(
      (e) => e.type == 'issue_created' || e.type == 'agent_action_pending');
});

/// Agent approval-queue pings (`agent_action_pending`,
/// `agent_action_decided`). The agent-actions approval screen reloads its
/// queue on either, so a proposal raised or decided elsewhere stays in sync.
final agentActionsChangedProvider = StreamProvider.autoDispose<WsEvent>((ref) {
  final events = ref.watch(realtimeEventsProvider);
  return events.where((e) =>
      e.type == 'agent_action_pending' || e.type == 'agent_action_decided');
});

/// Admin notice that the remediation auto-dispatch circuit breaker tripped and
/// disabled auto-dispatch (`remediation_autodispatch_disabled`). Surfaced as a
/// one-off in-app toast so an admin learns the agent stopped opening new auto
/// issues. The event carries `reason`/`giveups`/`threshold` (no issue id).
final autodispatchDisabledProvider = StreamProvider.autoDispose<WsEvent>((ref) {
  final events = ref.watch(realtimeEventsProvider);
  return events.where((e) => e.type == 'remediation_autodispatch_disabled');
});
