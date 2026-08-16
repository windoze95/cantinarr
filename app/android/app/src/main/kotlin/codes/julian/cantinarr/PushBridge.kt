package codes.julian.cantinarr

import io.flutter.plugin.common.MethodChannel

/// Shared push state between MainActivity (which owns the MethodChannel while
/// a Flutter engine is alive) and PushMessagingService (which runs with no
/// activity at all). The Android analog of the fields AppDelegate.swift keeps
/// on iOS: the channel, the single-slot cold-start tap payload, and the
/// "Dart's tap handler is wired" latch.
object PushBridge {
    /// Same channel as iOS — the Dart PushService is platform-neutral.
    const val CHANNEL_NAME = "codes.julian.cantinarr/push"

    /// The one notification channel. IMPORTANCE_HIGH (heads-up + sound) is the
    /// closest analog of iOS's always-banner presentation; per-category
    /// channels would duplicate the server-side notification preferences.
    /// Importance is frozen at first creation — changing it later needs a new
    /// channel id.
    const val CHANNEL_ID = "alerts"

    /// Marker extra distinguishing our notification tap intents from
    /// cantinarr:// deep links and plain launches.
    const val EXTRA_FROM_PUSH = "from_cantinarr_push"

    /// Data keys carrying the display strings (the gateway sends data-only
    /// messages); stripped before the remaining keys become the tap payload.
    const val KEY_TITLE = "notification_title"
    const val KEY_BODY = "notification_body"

    @Volatile
    var channel: MethodChannel? = null

    /// Tap payload from a notification that arrived before Dart was ready
    /// (cold start). Held until Dart pulls it via getInitialNotification.
    @Volatile
    var pendingTapPayload: Map<String, Any?>? = null

    /// True once Dart has called getInitialNotification, meaning its tap
    /// handler is wired and subsequent (warm) taps can be delivered live.
    @Volatile
    var dartTapReady = false
}
