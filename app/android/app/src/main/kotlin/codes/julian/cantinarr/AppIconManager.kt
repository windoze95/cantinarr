package codes.julian.cantinarr

import android.content.ComponentName
import android.content.Context
import android.content.pm.PackageManager
import android.os.Build
import io.flutter.plugin.common.MethodCall
import io.flutter.plugin.common.MethodChannel

/** Launcher aliases own the choice; MainActivity stays enabled for deep links
 * and notification taps regardless of the chosen icon. */
class AppIconManager(private val context: Context) {
    private val icons = linkedMapOf(
        "play-sun" to "PlaySunAlias",
        "play-sign" to "PlaySignAlias",
        "play-dome" to "PlayDomeAlias",
        "original" to "OriginalAlias",
    )

    private fun component(alias: String) = ComponentName(
        context.packageName,
        MainActivity::class.java.name.substringBeforeLast('.') + "." + alias,
    )

    private fun enabled(id: String, alias: String): Boolean =
        when (context.packageManager.getComponentEnabledSetting(component(alias))) {
            PackageManager.COMPONENT_ENABLED_STATE_ENABLED -> true
            PackageManager.COMPONENT_ENABLED_STATE_DEFAULT -> id == "play-sun"
            else -> false
        }

    private fun current(): String = icons.entries.firstOrNull { enabled(it.key, it.value) }?.key
        ?: "play-sun"

    fun handle(call: MethodCall, result: MethodChannel.Result) {
        try {
            when (call.method) {
                "isSupported" -> result.success(true)
                "getIcon" -> result.success(current())
                "setIcon" -> {
                    val id = call.argument<String>("icon")
                    val alias = icons[id]
                    if (alias == null) {
                        result.error("invalid_icon", "Unknown app icon", null)
                        return
                    }
                    val pm = context.packageManager
                    if (Build.VERSION.SDK_INT >= 33) {
                        pm.setComponentEnabledSettings(icons.map { (key, value) ->
                            PackageManager.ComponentEnabledSetting(
                                component(value),
                                if (key == id) PackageManager.COMPONENT_ENABLED_STATE_ENABLED
                                else PackageManager.COMPONENT_ENABLED_STATE_DISABLED,
                                PackageManager.DONT_KILL_APP,
                            )
                        })
                    } else {
                        // Always enable the destination first so the app cannot
                        // disappear from the launcher on older Android versions.
                        pm.setComponentEnabledSetting(component(alias),
                            PackageManager.COMPONENT_ENABLED_STATE_ENABLED, PackageManager.DONT_KILL_APP)
                        for ((key, value) in icons) {
                            if (key != id) pm.setComponentEnabledSetting(component(value),
                                PackageManager.COMPONENT_ENABLED_STATE_DISABLED, PackageManager.DONT_KILL_APP)
                        }
                    }
                    result.success(null)
                }
                else -> result.notImplemented()
            }
        } catch (_: Exception) {
            result.error("icon_change_failed", "Could not update the launcher icon", null)
        }
    }
}
