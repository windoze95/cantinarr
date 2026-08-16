package codes.julian.cantinarr

import android.Manifest
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Build
import android.os.Handler
import android.os.Looper
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import com.google.firebase.messaging.FirebaseMessagingService
import com.google.firebase.messaging.RemoteMessage

/// Receives FCM messages and token rotations. The gateway sends DATA-ONLY
/// messages (never an FCM `notification` block), so onMessageReceived fires in
/// foreground, background, and killed states alike and this service renders
/// every notification itself — one code path, matching iOS's always-banner
/// policy, and the tap PendingIntent (with its payload extras) is always built
/// by us rather than by the system tray.
class PushMessagingService : FirebaseMessagingService() {

    override fun onNewToken(token: String) {
        // Deliver to Dart when an engine is alive (it dedupes repeats). With no
        // engine — a background rotation — the next launch's getApnsToken pull
        // re-registers the fresh token.
        val channel = PushBridge.channel ?: return
        Handler(Looper.getMainLooper()).post {
            channel.invokeMethod("onApnsToken", token)
        }
    }

    override fun onMessageReceived(message: RemoteMessage) {
        val data = message.data
        val title = data[PushBridge.KEY_TITLE]
        val body = data[PushBridge.KEY_BODY]
        // No display fields = a background/content-only push: render nothing.
        if (title.isNullOrEmpty() && body.isNullOrEmpty()) return
        if (!canPostNotifications()) return

        ensureAlertsChannel(this)

        val tapIntent = Intent(this, MainActivity::class.java).apply {
            addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TOP)
            putExtra(PushBridge.EXTRA_FROM_PUSH, true)
            for ((key, value) in data) {
                if (key == PushBridge.KEY_TITLE || key == PushBridge.KEY_BODY) continue
                putExtra(key, value)
            }
        }
        // Distinct request codes keep distinct payloads: a shared code would
        // let one thread's PendingIntent extras overwrite another's.
        val requestCode = (message.collapseKey ?: data["type"] ?: "push").hashCode()
        val contentIntent = PendingIntent.getActivity(
            this,
            requestCode,
            tapIntent,
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )

        val notification = NotificationCompat.Builder(this, PushBridge.CHANNEL_ID)
            .setSmallIcon(R.drawable.ic_stat_notify)
            .setContentTitle(title ?: "")
            .setContentText(body ?: "")
            .setStyle(NotificationCompat.BigTextStyle().bigText(body ?: ""))
            .setPriority(NotificationCompat.PRIORITY_HIGH) // pre-O importance
            .setAutoCancel(true)
            .setContentIntent(contentIntent)
            .build()

        // Tag = collapse key, so a later push in the same thread REPLACES the
        // earlier one in the tray (the APNs apns-collapse-id behavior). No
        // collapse key → a unique id so unrelated alerts stack.
        val tag = message.collapseKey
        val id = if (tag != null) 0 else (System.currentTimeMillis() and 0x7FFFFFFF).toInt()
        NotificationManagerCompat.from(this).notify(tag, id, notification)
    }

    private fun canPostNotifications(): Boolean =
        Build.VERSION.SDK_INT < 33 ||
            checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) ==
            PackageManager.PERMISSION_GRANTED

    companion object {
        /// Idempotent; safe to call before every post and at activity startup.
        fun ensureAlertsChannel(context: Context) {
            if (Build.VERSION.SDK_INT < 26) return
            val manager = context.getSystemService(NotificationManager::class.java)
            if (manager.getNotificationChannel(PushBridge.CHANNEL_ID) != null) return
            manager.createNotificationChannel(
                NotificationChannel(
                    PushBridge.CHANNEL_ID,
                    "Alerts",
                    NotificationManager.IMPORTANCE_HIGH,
                ),
            )
        }
    }
}
