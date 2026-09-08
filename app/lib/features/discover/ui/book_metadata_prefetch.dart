import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../chaptarr/logic/book_metadata_loader.dart';

/// Only rendered, visible book rows participate. A settled viewport warms at
/// most three records, once; completing a fetch does not walk further down
/// the result list. Disposing the overlay retires queued work, not its cache.
class BookMetadataPrefetch extends ConsumerStatefulWidget {
  final Widget child;
  const BookMetadataPrefetch({super.key, required this.child});

  static void openBook(BuildContext context, BookMetadataKey key) {
    final owner =
        context.getInheritedWidgetOfExactType<_PrefetchScope>()?.owner;
    owner?._open(key);
  }

  @override
  ConsumerState<BookMetadataPrefetch> createState() =>
      _BookMetadataPrefetchState();
}

class _BookMetadataPrefetchState extends ConsumerState<BookMetadataPrefetch>
    with WidgetsBindingObserver {
  final _candidates = <_BookMetadataCandidateState>{};
  BookMetadataLoader? _loader;
  Timer? _pause;
  bool _opened = false;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) {
      _schedule();
    } else {
      _pause?.cancel();
      _loader?.clearPrefetch(this);
    }
  }

  void _schedule() {
    _pause?.cancel();
    _loader?.clearPrefetch(this);
    if (_opened) return;
    _pause = Timer(const Duration(milliseconds: 400), _warmVisible);
  }

  void _warmVisible() {
    final lifecycle = WidgetsBinding.instance.lifecycleState;
    if (!mounted ||
        _opened ||
        !(ModalRoute.of(context)?.isCurrent ?? true) ||
        (lifecycle != null && lifecycle != AppLifecycleState.resumed)) {
      return;
    }
    final viewport = context.findRenderObject();
    if (viewport is! RenderBox || !viewport.hasSize) return;
    final bounds = viewport.localToGlobal(Offset.zero) & viewport.size;
    final visible = <({BookMetadataKey key, double top})>[];
    for (final candidate in _candidates) {
      final box = candidate.context.findRenderObject();
      if (box is! RenderBox || !box.attached || !box.hasSize) continue;
      final rect = box.localToGlobal(Offset.zero) & box.size;
      if (rect.overlaps(bounds)) {
        visible.add((key: candidate.widget.metadataKey, top: rect.top));
      }
    }
    visible.sort((a, b) => a.top.compareTo(b.top));
    _loader?.prefetch(this, visible.map((item) => item.key).toSet().take(3));
  }

  void _open(BookMetadataKey key) {
    _opened = true;
    _pause?.cancel();
    // Claim before clearing the overlay so a queued selected book is promoted
    // rather than discarded. The detail screen joins this exact future.
    final loader = _loader;
    if (loader != null) {
      unawaited(loader.load(key));
      loader.clearPrefetch(this);
    }
  }

  @override
  void didUpdateWidget(covariant BookMetadataPrefetch oldWidget) {
    super.didUpdateWidget(oldWidget);
    // Returning to a still-mounted search overlay can resume its warmup.
    if (ModalRoute.of(context)?.isCurrent ?? true) _opened = false;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted) _schedule();
    });
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    _pause?.cancel();
    _loader?.clearPrefetch(this);
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final loader = ref.watch(bookMetadataLoaderProvider);
    if (!identical(loader, _loader)) {
      _loader?.clearPrefetch(this);
      _loader = loader;
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted) _schedule();
      });
    }
    return NotificationListener<ScrollMetricsNotification>(
      onNotification: (_) {
        _schedule();
        return false;
      },
      child: NotificationListener<ScrollNotification>(
        onNotification: (notification) {
          if (notification.depth != 0) return false;
          if (notification is ScrollEndNotification) {
            _schedule();
          } else if (notification is ScrollStartNotification ||
              notification is ScrollUpdateNotification) {
            _pause?.cancel();
            _loader?.clearPrefetch(this);
          }
          return false;
        },
        child: _PrefetchScope(owner: this, child: widget.child),
      ),
    );
  }
}

class _PrefetchScope extends InheritedWidget {
  final _BookMetadataPrefetchState owner;
  const _PrefetchScope({required this.owner, required super.child});
  @override
  bool updateShouldNotify(_PrefetchScope oldWidget) => owner != oldWidget.owner;
}

class BookMetadataCandidate extends StatefulWidget {
  final BookMetadataKey metadataKey;
  final Widget child;
  const BookMetadataCandidate(
      {super.key, required this.metadataKey, required this.child});
  @override
  State<BookMetadataCandidate> createState() => _BookMetadataCandidateState();
}

class _BookMetadataCandidateState extends State<BookMetadataCandidate> {
  _BookMetadataPrefetchState? _owner;
  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    final owner =
        context.dependOnInheritedWidgetOfExactType<_PrefetchScope>()?.owner;
    if (!identical(owner, _owner)) {
      _owner?._candidates.remove(this);
      _owner = owner;
      owner?._candidates.add(this);
    }
  }

  @override
  void dispose() {
    _owner?._candidates.remove(this);
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => widget.child;
}
