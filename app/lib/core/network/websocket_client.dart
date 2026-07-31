import 'dart:async';
import 'dart:convert';
import 'dart:math';
import 'package:flutter/foundation.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:web_socket_channel/web_socket_channel.dart';
import '../../features/auth/logic/auth_provider.dart';

/// Events emitted by the WebSocket connection.
class WsEvent {
  final String type;
  final Map<String, dynamic> data;

  const WsEvent({required this.type, required this.data});

  factory WsEvent.fromJson(Map<String, dynamic> json) => WsEvent(
        type: json['type'] as String? ?? 'unknown',
        data: json['data'] as Map<String, dynamic>? ?? {},
      );
}

/// Manages a persistent WebSocket connection to the backend with
/// automatic reconnection using exponential backoff.
///
/// The connection is lazy: nothing happens until [ensureConnected] is
/// called (typically by the realtime providers when first listened to).
/// The server URL and access token are read through callbacks at every
/// (re)connect so reconnects always use the freshest credentials, even
/// after a token refresh.
class WebSocketClient extends ChangeNotifier {
  final String? Function() _getServerUrl;
  final String? Function() _getAccessToken;

  /// Creates the underlying channel. Production always uses
  /// [WebSocketChannel.connect]; tests inject a factory so the
  /// connect → drop → reconnect lifecycle can be driven with fake streams
  /// instead of real sockets.
  @visibleForTesting
  final WebSocketChannel Function(Uri uri, {Iterable<String>? protocols})
      connectChannel;

  WebSocketChannel? _channel;
  StreamSubscription? _subscription;
  Timer? _reconnectTimer;
  int _reconnectAttempts = 0;
  bool _disposed = false;
  bool _connected = false;
  bool _started = false;

  final _eventController = StreamController<WsEvent>.broadcast();
  Stream<WsEvent> get events => _eventController.stream;
  bool get isConnected => _connected;

  WebSocketClient({
    required String? Function() getServerUrl,
    required String? Function() getAccessToken,
    WebSocketChannel Function(Uri uri, {Iterable<String>? protocols})?
        connectChannel,
  })  : _getServerUrl = getServerUrl,
        _getAccessToken = getAccessToken,
        connectChannel = connectChannel ?? WebSocketChannel.connect;

  /// Starts the connection on first call; subsequent calls are no-ops.
  /// Reconnection after that is handled internally with backoff.
  void ensureConnected() {
    if (_started || _disposed) return;
    _started = true;
    _connect();
  }

  void _connect() {
    if (_disposed) return;

    final serverUrl = _getServerUrl();
    final accessToken = _getAccessToken();
    if (serverUrl == null ||
        serverUrl.isEmpty ||
        accessToken == null ||
        accessToken.isEmpty) {
      // Not authenticated (yet) — don't handshake with credentials we know
      // are invalid; retry later (login also recreates this client).
      _scheduleReconnect();
      return;
    }

    try {
      final wsUrl = serverUrl
          .replaceFirst('https://', 'wss://')
          .replaceFirst('http://', 'ws://');

      final channel = connectChannel(
        Uri.parse('$wsUrl/api/ws'),
        protocols: ['Bearer', accessToken],
      );
      _channel = channel;

      _subscription = channel.stream.listen(
        _onMessage,
        onDone: _onDisconnected,
        onError: (_) => _onDisconnected(),
      );

      // Only mark connected once the upgrade handshake completes; failures
      // surface through the stream's onError/onDone above.
      channel.ready.then((_) {
        if (_disposed || !identical(channel, _channel)) return;
        _connected = true;
        _reconnectAttempts = 0;
        notifyListeners();
      }).catchError((_) {
        // Handled via the stream's onError -> _onDisconnected.
      });
    } catch (e) {
      debugPrint('WebSocket connection failed: $e');
      _scheduleReconnect();
    }
  }

  void _onMessage(dynamic message) {
    try {
      final json = jsonDecode(message as String) as Map<String, dynamic>;
      _eventController.add(WsEvent.fromJson(json));
    } catch (e) {
      debugPrint('WebSocket message parse error: $e');
    }
  }

  void _onDisconnected() {
    if (_disposed) return;
    if (_connected) {
      _connected = false;
      notifyListeners();
    }
    _scheduleReconnect();
  }

  void _scheduleReconnect() {
    if (_disposed) return;
    // A failed connect can report both onError and onDone; don't let that
    // double-schedule or double-increment the backoff.
    if (_reconnectTimer?.isActive ?? false) return;

    // Exponential backoff: 1s, 2s, 4s, 8s, ... up to 30s. Clamp the
    // exponent, not the result: pow() overflows to infinity after ~1024
    // straight failures and infinity.toInt() throws, which would kill the
    // reconnect chain permanently.
    final delay = Duration(
      seconds: min(1 << min(_reconnectAttempts, 5), 30),
    );
    _reconnectAttempts++;

    _reconnectTimer = Timer(delay, _connect);
  }

  @override
  void dispose() {
    _disposed = true;
    _reconnectTimer?.cancel();
    _subscription?.cancel();
    _channel?.sink.close();
    _eventController.close();
    super.dispose();
  }
}

/// Provides the app-wide WebSocket client.
///
/// The client is created dormant and only connects once something calls
/// [WebSocketClient.ensureConnected] (see realtime_provider.dart). It is
/// recreated on login/logout/server switch (server URL changes); token
/// refreshes are picked up through the getter on the next reconnect without
/// tearing down a live connection.
final webSocketClientProvider = ChangeNotifierProvider<WebSocketClient>((ref) {
  // Rebuild only when the server URL changes — not on every auth update
  // (token refreshes would otherwise churn the connection).
  ref.watch(authProvider.select((s) => s.valueOrNull?.connection?.serverUrl));

  return WebSocketClient(
    getServerUrl: () =>
        ref.read(authProvider).valueOrNull?.connection?.serverUrl,
    getAccessToken: () =>
        ref.read(authProvider).valueOrNull?.connection?.accessToken,
  );
});
