package cc.elysia.foyer

import android.content.Context
import java.net.URI

/** 服务器地址等本地配置。 */
class ConfigStore(context: Context) {

    private val sp = context.getSharedPreferences("foyer", Context.MODE_PRIVATE)

    var baseUrl: String?
        get() = sp.getString(KEY_BASE, null)
        set(value) = sp.edit().putString(KEY_BASE, value).apply()

    fun clear() = sp.edit().remove(KEY_BASE).apply()

    companion object {
        private const val KEY_BASE = "base_url"

        /** 规范化用户输入：补 scheme、去末尾斜杠，非法输入返回 null。 */
        fun normalize(input: String): String? {
            var s = input.trim()
            if (s.isEmpty()) return null
            if (!s.startsWith("http://") && !s.startsWith("https://")) {
                s = "http://$s"
            }
            s = s.trimEnd('/')
            return try {
                val uri = URI(s)
                if (uri.host.isNullOrEmpty()) null else s
            } catch (e: Exception) {
                null
            }
        }
    }
}
