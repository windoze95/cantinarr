package codes.julian.cantinarr

import android.app.Activity
import android.content.ActivityNotFoundException
import android.content.Intent
import android.net.Uri

/** Opens the supported media clients, with their home screen as a fallback. */
class MediaAppLauncher(private val activity: Activity) {
    fun open(serviceType: String?, titleUrl: String?): Boolean {
        val packageName = when (serviceType) {
            "plex" -> "com.plexapp.android"
            "jellyfin" -> "org.jellyfin.mobile"
            "emby" -> "com.mb.android"
            "audiobookshelf" -> "com.audiobookshelf.app"
            "theshelf" -> "rmc.theshelf.player"
            else -> return false
        }

        // Jellyfin has no title URL handler. Plex and Emby can accept a VIEW
        // intent, but old/new versions may only support launching their home.
        if (serviceType in setOf("plex", "emby") && !titleUrl.isNullOrEmpty()) {
            val uri = Uri.parse(titleUrl)
            if (uri.scheme == serviceType &&
                start(Intent(Intent.ACTION_VIEW, uri).setPackage(packageName))) {
                return true
            }
        }

        val home = try {
            activity.packageManager.getLaunchIntentForPackage(packageName)
        } catch (_: SecurityException) {
            null
        }
        return home != null && start(home)
    }

    private fun start(intent: Intent): Boolean = try {
        activity.startActivity(intent)
        true
    } catch (_: ActivityNotFoundException) {
        false
    } catch (_: SecurityException) {
        false
    }
}
