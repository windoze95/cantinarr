import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/chaptarr/data/chaptarr_models.dart';
import 'package:cantinarr/features/chaptarr/logic/book_metadata_loader.dart';
import 'package:cantinarr/features/discover/ui/book_metadata_prefetch.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  testWidgets(
      'only three visible rows warm after a pause, scrolling schedules a new viewport',
      (tester) async {
    final calls = <BookMetadataKey>[];
    final loader = BookMetadataLoader((key, _) async {
      calls.add(key);
      return [ChaptarrBook(id: 0, title: key.foreignId)];
    });
    final scroll = ScrollController();
    await tester.pumpWidget(ProviderScope(
        overrides: [
          bookMetadataLoaderProvider.overrideWithValue(loader),
        ],
        child: MaterialApp(
            home: Scaffold(
                body: SizedBox(
          height: 450,
          child: BookMetadataPrefetch(
              child: ListView.builder(
            controller: scroll,
            itemCount: 30,
            itemExtent: 100,
            itemBuilder: (_, i) => BookMetadataCandidate(
              metadataKey: (instanceId: 'books', foreignId: 'gr:$i'),
              child: Text('Book $i'),
            ),
          )),
        )))));
    await tester.pump(const Duration(milliseconds: 399));
    expect(calls, isEmpty);
    await tester.pump(const Duration(milliseconds: 1));
    expect(calls.map((key) => key.foreignId), ['gr:0', 'gr:1', 'gr:2']);
    await tester.pump(const Duration(seconds: 1));
    expect(calls, hasLength(3));
    scroll.jumpTo(1000);
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 400));
    expect(
        calls.skip(3).map((key) => key.foreignId), ['gr:10', 'gr:11', 'gr:12']);
    await tester.pumpWidget(const SizedBox());
    loader.dispose();
    scroll.dispose();
    await tester.pump();
  });

  testWidgets(
      'opening a not-yet-warmed row starts immediately and survives overlay disposal',
      (tester) async {
    final reply = Completer<List<ChaptarrBook>>();
    final calls = <BookMetadataKey>[];
    final loader = BookMetadataLoader((key, _) {
      calls.add(key);
      return reply.future;
    });
    const key = (instanceId: 'books', foreignId: 'gr:1');
    await tester.pumpWidget(ProviderScope(
        overrides: [bookMetadataLoaderProvider.overrideWithValue(loader)],
        child: MaterialApp(
            home: BookMetadataPrefetch(
                child: Builder(
                    builder: (context) => TextButton(
                        onPressed: () =>
                            BookMetadataPrefetch.openBook(context, key),
                        child: const Text('Open book')))))));
    await tester.tap(find.text('Open book'));
    expect(calls, [key]);
    await tester.pumpWidget(const SizedBox());
    final opened = loader.load(key);
    reply.complete([const ChaptarrBook(id: 0, title: 'Full details')]);
    await tester.pump();
    expect((await opened).books.single.title, 'Full details');
    expect(calls, [key]);
    loader.dispose();
  });

  test(
      'token refresh shares cache; account and grant changes discard it and cancel requests',
      () async {
    final adapter = _HeldAdapter();
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = adapter;
    final transportErrors = <Object>[];
    dio.interceptors.add(InterceptorsWrapper(onError: (error, handler) {
      transportErrors.add(error);
      handler.next(error);
    }));
    final container = ProviderContainer(overrides: [
      authProvider.overrideWith(_ScopeAuth.new),
      backendClientProvider.overrideWithValue(dio),
    ]);
    addTearDown(container.dispose);
    await container.read(authProvider.future);
    await container.pump();
    final old = container.read(bookMetadataLoaderProvider);
    const key = (instanceId: 'books', foreignId: 'gr:1');
    final previous = old.load(key);
    await adapter.firstRequest.future;
    expect(transportErrors, isEmpty);
    expect(adapter.requests, hasLength(1),
        reason: 'initial lookup reaches the adapter');
    final auth = container.read(authProvider.notifier) as _ScopeAuth;
    auth.replace(_auth(token: 'refreshed'));
    await container.pump();
    expect(container.read(bookMetadataLoaderProvider), same(old));
    auth.replace(_auth(user: 2));
    await container.pump();
    final current = container.read(bookMetadataLoaderProvider);
    expect(current, isNot(same(old)));
    expect((await previous).cancelled, isTrue);
    final next = current.load(key);
    await adapter.secondRequest.future;
    expect(adapter.requests, hasLength(2),
        reason: 'new account starts its own lookup');
    adapter.complete(0, 'Previous account');
    adapter.complete(1, 'Current account');
    expect((await next).books.single.title, 'Current account');
    expect(old.peek(key), isNull);
    auth.replace(_auth(user: 2, grant: false));
    await container.pump();
    expect(current.peek(key), isNull);
    expect((await container.read(bookMetadataLoaderProvider).load(key)).failed,
        isTrue);
    expect(adapter.requests, hasLength(2));
  });
}

AuthState _auth({int user = 1, String token = 'token', bool grant = true}) =>
    AuthState(
      user: UserProfile(
          id: user,
          username: 'reader',
          role: 'user',
          permissions: const ['media:discover']),
      connection: BackendConnection(
          serverUrl: 'http://localhost',
          accessToken: token,
          refreshToken: 'refresh',
          instances: grant
              ? const [
                  ServiceInstance(
                      id: 'books', name: 'Books', serviceType: 'chaptarr')
                ]
              : const []),
    );

class _ScopeAuth extends AuthNotifier {
  @override
  Future<AuthState> build() async => _auth();
  void replace(AuthState next) => state = AsyncData(next);
}

class _HeldAdapter implements HttpClientAdapter {
  final requests = <Completer<ResponseBody>>[];
  final firstRequest = Completer<void>();
  final secondRequest = Completer<void>();
  @override
  Future<ResponseBody> fetch(RequestOptions options,
      Stream<Uint8List>? requestStream, Future<void>? cancelFuture) {
    final reply = Completer<ResponseBody>();
    requests.add(reply);
    if (requests.length == 1) firstRequest.complete();
    if (requests.length == 2) secondRequest.complete();
    return reply.future;
  }

  void complete(int index, String title) =>
      requests[index].complete(ResponseBody.fromString(
          jsonEncode([
            {'title': title, 'foreignBookId': 'gr:1'}
          ]),
          200,
          headers: {
            'content-type': ['application/json']
          }));
  @override
  void close({bool force = false}) {}
}
