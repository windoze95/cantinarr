import 'dart:async';

import 'package:cantinarr/core/network/websocket_client.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:web_socket_channel/web_socket_channel.dart';

/// Reconnect/backoff behavior of [WebSocketClient], driven entirely through
/// the injected channel factory and the widget test binding's fake clock —
/// no real sockets, no wall-clock timing.
///
/// What the client does on reconnect (and what these tests pin): it redials
/// with the freshest credentials from its getters, re-pipes the new channel
/// into the same broadcast [WebSocketClient.events] stream (so consumer
/// subscriptions survive the drop), and flips [WebSocketClient.isConnected].
/// It performs NO data backfill of its own — consumers are documented as
/// best-effort listeners that keep a REST polling / foreground-refresh
/// fallback for anything missed while the socket was down.
void main() {
  testWidgets(
      'connects lazily, marks connected only after the handshake, and '
      'keeps the event stream alive through malformed frames', (tester) async {
    final harness = _Harness();
    addTearDown(harness.dispose);

    // Dormant until ensureConnected.
    expect(harness.factory.channels, isEmpty);

    harness.client.ensureConnected();
    expect(harness.factory.channels, hasLength(1));
    expect(
      harness.factory.uris.single.toString(),
      'wss://media.example.com/api/ws',
      reason: 'https:// must be dialed as wss://',
    );
    expect(harness.factory.protocols.single, ['Bearer', 'token-1']);
    expect(harness.client.isConnected, isFalse,
        reason: 'not connected until the upgrade handshake completes');

    // A second call must not dial a second connection.
    harness.client.ensureConnected();
    expect(harness.factory.channels, hasLength(1));

    harness.factory.current.readyCompleter.complete();
    await tester.pump();
    expect(harness.client.isConnected, isTrue);

    // A malformed frame is swallowed; a valid one still comes through after.
    harness.factory.current.controller.add('{not json');
    harness.factory.current.controller
        .add('{"type":"request_decision","data":{"decision":"approved"}}');
    await tester.pump();
    expect(harness.events, hasLength(1));
    expect(harness.events.single.type, 'request_decision');
    expect(harness.events.single.data['decision'], 'approved');
  });

  testWidgets(
      'a dropped connection reconnects after 1s with fresh credentials and '
      'feeds the same event stream', (tester) async {
    final harness = _Harness();
    addTearDown(harness.dispose);

    harness.client.ensureConnected();
    harness.factory.current.readyCompleter.complete();
    await tester.pump();
    expect(harness.client.isConnected, isTrue);

    // A token refresh lands while connected; the reconnect must pick it up
    // through the getter (the live connection is not torn down for it).
    harness.accessToken = 'token-2';
    expect(harness.factory.channels, hasLength(1));

    // Server drops the connection.
    await harness.factory.current.controller.close();
    await tester.pump();
    expect(harness.client.isConnected, isFalse);
    expect(harness.factory.channels, hasLength(1),
        reason: 'reconnect waits for the backoff delay');

    await tester.pump(const Duration(milliseconds: 999));
    expect(harness.factory.channels, hasLength(1));
    await tester.pump(const Duration(milliseconds: 1));
    expect(harness.factory.channels, hasLength(2),
        reason: 'first reconnect fires after 1s');
    expect(harness.factory.protocols.last, ['Bearer', 'token-2'],
        reason: 'reconnects must use the freshest access token');

    harness.factory.current.readyCompleter.complete();
    await tester.pump();
    expect(harness.client.isConnected, isTrue);

    // Consumers' existing subscription keeps receiving events from the NEW
    // channel — reconnection re-pipes into the same broadcast stream.
    harness.factory.current.controller
        .add('{"type":"arr_queue_changed","data":{"instance_id":"r1"}}');
    await tester.pump();
    expect(harness.events.map((e) => e.type), ['arr_queue_changed']);

    // A successful handshake reset the backoff: the next drop redials
    // after 1s again, not after a grown delay.
    await harness.factory.current.controller.close();
    await tester.pump();
    await tester.pump(const Duration(seconds: 1));
    expect(harness.factory.channels, hasLength(3));
  });

  testWidgets(
      'failed attempts back off exponentially (1,2,4,8,16s) and clamp at 30s',
      (tester) async {
    final harness = _Harness();
    addTearDown(harness.dispose);

    harness.client.ensureConnected();
    expect(harness.factory.channels, hasLength(1));
    await harness.failCurrent(tester);

    // After the k-th consecutive failure the next dial happens after
    // min(2^k, 30) seconds. A failed dial reports both onError and onDone;
    // if that ever double-incremented the backoff this sequence would read
    // 2,8,32… instead.
    const delays = [1, 2, 4, 8, 16, 30, 30];
    var dials = 1;
    for (final seconds in delays) {
      await tester.pump(Duration(seconds: seconds) -
          const Duration(milliseconds: 1));
      expect(harness.factory.channels, hasLength(dials),
          reason: 'no redial before the full ${seconds}s backoff');
      await tester.pump(const Duration(milliseconds: 1));
      dials++;
      expect(harness.factory.channels, hasLength(dials),
          reason: 'redial after ${seconds}s');
      await harness.failCurrent(tester);
    }

    expect(harness.client.isConnected, isFalse);

    // The next (pending) retry timer would trip the binding's timer
    // invariant; disposing here cancels it (also proving cancel-on-dispose).
    harness.dispose();
  });

  testWidgets('never dials without credentials; dials once they appear',
      (tester) async {
    final harness = _Harness(serverUrl: null, accessToken: null);
    addTearDown(harness.dispose);

    harness.client.ensureConnected();
    expect(harness.factory.channels, isEmpty,
        reason: 'no handshake with credentials known to be absent');

    // Retries on the same backoff schedule, still without dialing.
    await tester.pump(const Duration(seconds: 1));
    expect(harness.factory.channels, isEmpty);

    // Credentials appear (login); the next retry dials with them.
    harness.serverUrl = 'http://10.0.0.5:8585';
    harness.accessToken = 'fresh';
    await tester.pump(const Duration(seconds: 2));
    expect(harness.factory.channels, hasLength(1));
    expect(
      harness.factory.uris.single.toString(),
      'ws://10.0.0.5:8585/api/ws',
      reason: 'http:// must be dialed as ws://',
    );
    expect(harness.factory.protocols.single, ['Bearer', 'fresh']);
  });

  testWidgets('dispose stops the reconnect chain and closes the channel',
      (tester) async {
    final harness = _Harness();

    harness.client.ensureConnected();
    harness.factory.current.readyCompleter.complete();
    await tester.pump();
    await harness.factory.current.controller.close();
    await tester.pump();

    final sink = harness.factory.current.channelSink;
    harness.client.dispose();
    expect(sink.closed, isTrue);

    await tester.pump(const Duration(minutes: 5));
    expect(harness.factory.channels, hasLength(1),
        reason: 'a disposed client must never redial');
  });
}

/// A [WebSocketClient] wired to a recording channel factory and mutable
/// credential getters, with a listener collecting every emitted [WsEvent].
class _Harness {
  _Harness({
    this.serverUrl = 'https://media.example.com',
    this.accessToken = 'token-1',
  }) {
    client = WebSocketClient(
      getServerUrl: () => serverUrl,
      getAccessToken: () => accessToken,
      connectChannel: factory.call,
    );
    _sub = client.events.listen(events.add);
  }

  final factory = _ChannelFactory();
  final events = <WsEvent>[];
  String? serverUrl;
  String? accessToken;
  late final WebSocketClient client;
  late final StreamSubscription<WsEvent> _sub;
  bool _disposed = false;

  /// Fails the newest channel the way a refused/killed socket does: an error
  /// followed by done.
  Future<void> failCurrent(WidgetTester tester) async {
    factory.current.controller.addError(const SocketFault());
    await factory.current.controller.close();
    await tester.pump();
  }

  void dispose() {
    if (_disposed) return;
    _disposed = true;
    _sub.cancel();
    client.dispose();
  }
}

/// Marker error used to fail fake connections.
class SocketFault implements Exception {
  const SocketFault();
}

class _ChannelFactory {
  final List<_FakeChannel> channels = [];
  final List<Uri> uris = [];
  final List<List<String>> protocols = [];

  _FakeChannel get current => channels.last;

  WebSocketChannel call(Uri uri, {Iterable<String>? protocols}) {
    uris.add(uri);
    this.protocols.add(protocols?.toList() ?? const []);
    final channel = _FakeChannel();
    channels.add(channel);
    return channel;
  }
}

/// Only the members [WebSocketClient] touches are real: [stream], [ready],
/// and [sink].close(). Everything else throws via [Fake].
class _FakeChannel extends Fake implements WebSocketChannel {
  final StreamController<dynamic> controller = StreamController<dynamic>();
  final Completer<void> readyCompleter = Completer<void>();
  final _FakeSink channelSink = _FakeSink();

  @override
  Stream<dynamic> get stream => controller.stream;

  @override
  Future<void> get ready => readyCompleter.future;

  @override
  WebSocketSink get sink => channelSink;
}

class _FakeSink extends Fake implements WebSocketSink {
  bool closed = false;

  @override
  Future<dynamic> close([int? closeCode, String? closeReason]) async {
    closed = true;
  }
}
