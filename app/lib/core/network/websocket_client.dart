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
class WebSocketClient extends ChangeNotifier {
  final String _serverUrl;
  final String _accessToken;

  WebSocketChannel? _channel;
  StreamSubscription? _subscription;
  Timer? _reconnectTimer;
  int _reconnectAttempts = 0;
  bool _disposed = false;
  bool _connected = false;

  final _eventController = StreamController<WsEvent>.broadcast();
  Stream<WsEvent> get events => _eventController.stream;
  bool get isConnected => _connected;

  WebSocketClient({
    required String serverUrl,
    required String accessToken,
  })  : _serverUrl = serverUrl,
        _accessToken = accessToken {
    _connect();
  }

  void _connect() {
    if (_disposed) return;

    try {
      final wsUrl = _serverUrl
          .replaceFirst('https://', 'wss://')
          .replaceFirst('http://', 'ws://');

      _channel = WebSocketChannel.connect(
        Uri.parse('$wsUrl/api/ws'),
        protocols: ['Bearer', _accessToken],
      );

      _subscription = _channel!.stream.listen(
        _onMessage,
        onDone: _onDisconnected,
        onError: (_) => _onDisconnected(),
      );

      _connected = true;
      _reconnectAttempts = 0;
      notifyListeners();
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
    _connected = false;
    notifyListeners();
    _scheduleReconnect();
  }

  void _scheduleReconnect() {
    if (_disposed) return;
    _reconnectTimer?.cancel();

    // Exponential backoff: 1s, 2s, 4s, 8s, ... up to 30s
    final delay = Duration(
      seconds: min(pow(2, _reconnectAttempts).toInt(), 30),
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

/// Provides a WebSocket client connected to the backend.
final webSocketClientProvider = ChangeNotifierProvider<WebSocketClient>((ref) {
  final authState = ref.watch(authProvider);
  final connection = authState.valueOrNull?.connection;

  if (connection == null) {
    return WebSocketClient(serverUrl: '', accessToken: '');
  }

  return WebSocketClient(
    serverUrl: connection.serverUrl,
    accessToken: connection.accessToken,
  );
});
