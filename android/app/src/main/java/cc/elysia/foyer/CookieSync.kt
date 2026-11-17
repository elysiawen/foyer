package cc.elysia.foyer

import android.webkit.CookieManager
import java.net.HttpURLConnection
import java.net.URI

/** 原生请求与 WebView 之间的会话 cookie 同步。 */
object CookieSync {

    /** 请求前把 WebView 持有的 cookie 附加到原生请求上。 */
    fun attach(base: String, conn: HttpURLConnection) {
        val cookie = CookieManager.getInstance().getCookie(base)
        if (!cookie.isNullOrEmpty()) {
            conn.setRequestProperty("Cookie", cookie)
        }
    }

    /** 把响应里的 Set-Cookie 写回 WebView 的 CookieManager。 */
    fun store(base: String, conn: HttpURLConnection) {
        val cm = CookieManager.getInstance()
        val host = runCatching { URI(base).host?.lowercase() }.getOrNull().orEmpty()
        conn.headerFields.forEach { (key, values) ->
            if (key != null && key.equals("Set-Cookie", ignoreCase = true)) {
                values?.forEach { cm.setCookie(base, matchHost(it, host)) }
            }
        }
        cm.flush()
    }

    /**
     * 服务端固定下发 Domain=门户域名时，用局域网 IP 访问会因主机不匹配
     * 导致 cookie 被 CookieManager 直接丢弃，表现成登录成功后又被踢回登录页。
     * 这里去掉不匹配的 Domain 属性，按 host-only cookie 保存。
     */
    private fun matchHost(cookie: String, host: String): String {
        val parts = cookie.split(';')
        val domain = parts.firstOrNull { it.trim().startsWith("Domain=", ignoreCase = true) }
            ?.substringAfter('=')?.trim()?.removePrefix(".")?.lowercase()
        if (domain.isNullOrEmpty() || host.isEmpty()) return cookie
        if (host == domain || host.endsWith(".$domain")) return cookie
        return parts
            .filterNot { it.trim().startsWith("Domain=", ignoreCase = true) }
            .joinToString(";")
    }

    fun clearAll(done: () -> Unit = {}) {
        CookieManager.getInstance().removeAllCookies { done() }
    }
}
