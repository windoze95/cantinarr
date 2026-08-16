package codes.julian.cantinarr

import android.Manifest
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.provider.Settings
import androidx.core.app.NotificationManagerCompat
import com.google.firebase.messaging.FirebaseMessaging
import io.flutter.embedding.android.FlutterActivity
import io.flutter.embedding.engine.FlutterEngine
import io.flutter.plugin.common.MethodCall
import io.flutter.plugin.common.MethodChannel

/// Android half of the `codes.julian.cantinarr/push` MethodChannel, mirroring
/// ios/Runner/AppDelegate.swift: the same method names (the "Apns" in
/// getApnsToken/onApnsToken is historical — Dart treats the token as opaque;
/// here it is an FCM registration token) and the same cold/warm tap state
/// machine. Message display lives in PushMessagingService; this activity owns
/// permission flow, token pulls, and tap capture/delivery.
class MainActivity : FlutterActivity() {

    private var pendingPermissionResult: MethodChannel.Result? = null

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        PushMessagingService.ensureAlertsChannel(this)
        captureTapFromIntent(intent)
    }

    override fun configureFlutterEngine(flutterEngine: FlutterEngine) {
        super.configureFlutterEngine(flutterEngine)
        val channel = MethodChannel(
            flutterEngine.dartExecutor.binaryMessenger,
            PushBridge.CHANNEL_NAME,
        )
        channel.setMethodCallHandler { call, result -> handleMethodCall(call, result) }
        PushBridge.channel = channel
    }

    override fun cleanUpFlutterEngine(flutterEngine: FlutterEngine) {
        // The engine (and with it Dart's tap handler) is going away; a tap that
        // arrives now must cold-start rather than post to a dead channel.
        PushBridge.channel = null
        PushBridge.dartTapReady = false
        super.cleanUpFlutterEngine(flutterEngine)
    }

    override fun onNewIntent(intent: Intent) {
        // Super first: FlutterActivity forwards cantinarr:// deep links to the
        // engine. Our push intents carry only extras (no data URI), so the two
        // paths never collide.
        super.onNewIntent(intent)
        captureTapFromIntent(intent)
    }

    // MARK: - Dart -> Native

    private fun handleMethodCall(call: MethodCall, result: MethodChannel.Result) {
        when (call.method) {
            "requestPermission" -> requestNotificationPermission(result)
            "getApnsToken" ->
                // FCM has a pull API (unlike APNs' callback-only delivery), so
                // there is no native cache: resolve the live token. Fails null
                // (never throws to Dart) so de-Googled devices degrade silently.
                FirebaseMessaging.getInstance().token
                    .addOnSuccessListener { token -> result.success(token) }
                    .addOnFailureListener { result.success(null) }
            "getAuthorizationStatus" -> result.success(authorizationStatus())
            "openNotificationSettings" -> result.success(openNotificationSettings())
            "setBadgeCount" ->
                // Android has no public numeric app-icon badge API; channel
                // notification dots are automatic. Deliberate no-op.
                result.success(null)
            "getInitialNotification" -> {
                // Dart pulls the cold-start tap (if any) and signals it's ready
                // for live taps from here on.
                PushBridge.dartTapReady = true
                val payload = PushBridge.pendingTapPayload
                PushBridge.pendingTapPayload = null
                result.success(payload)
            }
            else -> result.notImplemented()
        }
    }

    // MARK: - Tap capture

    /// Extracts a push tap payload from an activity intent. Live-delivers it
    /// when Dart's handler is wired (warm tap), else stashes it for
    /// getInitialNotification (cold start) — the AppDelegate state machine.
    private fun captureTapFromIntent(intent: Intent?) {
        if (intent == null) return
        if (!intent.getBooleanExtra(PushBridge.EXTRA_FROM_PUSH, false)) return
        // Recents redelivers the historical launch intent: without this guard,
        // every reopen from recents would re-route to a stale notification.
        if (intent.flags and Intent.FLAG_ACTIVITY_LAUNCHED_FROM_HISTORY != 0) return

        val extras = intent.extras ?: return
        val payload = mutableMapOf<String, Any?>()
        for (key in extras.keySet()) {
            if (key == PushBridge.EXTRA_FROM_PUSH) continue
            // FCM data values are strings; anything else here is a system extra.
            val value = extras.getString(key) ?: continue
            payload[key] = value
        }
        // Consume the marker so a redelivered copy of this intent can't replay.
        intent.removeExtra(PushBridge.EXTRA_FROM_PUSH)

        val channel = PushBridge.channel
        if (PushBridge.dartTapReady && channel != null) {
            channel.invokeMethod("onNotificationTap", payload)
        } else {
            PushBridge.pendingTapPayload = payload
        }
    }

    // MARK: - Permission

    /// Requests POST_NOTIFICATIONS (API 33+). Matches the iOS semantics: the
    /// system dialog is shown at most once — after a denial, later calls
    /// resolve false immediately and recovery goes through the system settings
    /// deep link, exactly like iOS's silent repeat requestAuthorization.
    private fun requestNotificationPermission(result: MethodChannel.Result) {
        val enabled = NotificationManagerCompat.from(this).areNotificationsEnabled()
        if (Build.VERSION.SDK_INT < 33 || enabled) {
            // Pre-33 has no runtime dialog (notifications default on; the user
            // can only toggle them off in settings).
            markPrompted()
            result.success(enabled)
            return
        }
        if (hasPrompted()) {
            result.success(false)
            return
        }
        if (pendingPermissionResult != null) {
            result.success(false) // a concurrent second request; deny quietly
            return
        }
        pendingPermissionResult = result
        requestPermissions(arrayOf(Manifest.permission.POST_NOTIFICATIONS), REQUEST_NOTIFICATIONS)
    }

    override fun onRequestPermissionsResult(
        requestCode: Int,
        permissions: Array<out String>,
        grantResults: IntArray,
    ) {
        super.onRequestPermissionsResult(requestCode, permissions, grantResults)
        if (requestCode != REQUEST_NOTIFICATIONS) return
        markPrompted()
        val granted =
            grantResults.isNotEmpty() && grantResults[0] == PackageManager.PERMISSION_GRANTED
        pendingPermissionResult?.success(granted)
        pendingPermissionResult = null
    }

    /// Status strings the Dart side interprets (same vocabulary as iOS;
    /// provisional/ephemeral never occur on Android). `notDetermined` is
    /// derived from the persisted prompted flag — Android itself doesn't
    /// distinguish "never asked" from "denied". The flag rides device backup
    /// (allowBackup default), so a restored install may read `denied` until
    /// the user grants via settings — accepted, matches the no-re-prompt rule.
    private fun authorizationStatus(): String {
        if (NotificationManagerCompat.from(this).areNotificationsEnabled()) return "authorized"
        if (Build.VERSION.SDK_INT >= 33 && !hasPrompted()) return "notDetermined"
        return "denied"
    }

    private fun openNotificationSettings(): Boolean = try {
        val intent = if (Build.VERSION.SDK_INT >= 26) {
            Intent(Settings.ACTION_APP_NOTIFICATION_SETTINGS)
                .putExtra(Settings.EXTRA_APP_PACKAGE, packageName)
        } else {
            Intent(
                Settings.ACTION_APPLICATION_DETAILS_SETTINGS,
                Uri.fromParts("package", packageName, null),
            )
        }
        startActivity(intent)
        true
    } catch (_: Exception) {
        false
    }

    private fun prefs() = getSharedPreferences("cantinarr_push", MODE_PRIVATE)

    private fun hasPrompted() = prefs().getBoolean(PREF_PROMPTED, false)

    private fun markPrompted() {
        prefs().edit().putBoolean(PREF_PROMPTED, true).apply()
    }

    companion object {
        private const val REQUEST_NOTIFICATIONS = 4271
        private const val PREF_PROMPTED = "prompted"
    }
}
