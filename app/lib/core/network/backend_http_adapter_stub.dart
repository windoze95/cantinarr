import 'package:dio/dio.dart';

HttpClientAdapter? createBackendHttpClientAdapter() => null;

/// Native platforms keep Dio's dart:io adapter for auth calls; only web needs
/// the Fetch transport override.
HttpClientAdapter? createAuthHttpClientAdapter() => null;
