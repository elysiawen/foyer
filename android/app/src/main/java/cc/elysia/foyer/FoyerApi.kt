package cc.elysia.foyer

import java.io.IOException
import java.net.HttpURLConnection
import java.net.URL
import java.net.URLEncoder

/**
 * 与 Foyer 服务端的 HTTP 交互。
 * 全部在后台线程调用；登录/登出的 303 是成功，列表的 303 代表会话失效。
 */
object FoyerApi {

    private const val UA = "FoyerAndroid/1.0"
    private const val TIMEOUT = 15_000

    sealed interface LoginResult {
        data object Success : LoginResult
        data class NeedOtp(val token: String) : LoginResult
        data class Failure(val message: String) : LoginResult
    }

    sealed interface AppsResult {
        data class Success(val apps: List<PortalParser.AppItem>) : AppsResult
        data object SessionExpired : AppsResult
        data class Failure(val message: String) : AppsResult
    }

    private data class Response(
        val code: Int,
        val body: String,
        val location: String?,
    )

    private fun request(
        base: String,
        path: String,
        form: Map<String, String>? = null,
    ): Response {
        val conn = URL(base + path).openConnection() as HttpURLConnection
        try {
            conn.connectTimeout = TIMEOUT
            conn.readTimeout = TIMEOUT
            conn.instanceFollowRedirects = false
            conn.setRequestProperty("User-Agent", UA)
            CookieSync.attach(base, conn)

            if (form != null) {
                conn.requestMethod = "POST"
                conn.doOutput = true
                conn.setRequestProperty("Origin", base)
                conn.setRequestProperty(
                    "Content-Type", "application/x-www-form-urlencoded; charset=UTF-8"
                )
                val body = form.entries.joinToString("&") { (k, v) ->
                    URLEncoder.encode(k, "UTF-8") + "=" + URLEncoder.encode(v, "UTF-8")
                }
                conn.outputStream.use { it.write(body.toByteArray(Charsets.UTF_8)) }
            }

            val code = conn.responseCode
            CookieSync.store(base, conn)
            val stream = if (code in 200..399) {
                try { conn.inputStream } catch (e: IOException) { null }
            } else {
                conn.errorStream
            }
            val text = stream?.use { String(it.readBytes(), Charsets.UTF_8) }.orEmpty()
            return Response(code, text, conn.getHeaderField("Location"))
        } finally {
            conn.disconnect()
        }
    }

    /** 连通性检查，返回 null 表示正常，否则为错误信息。 */
    fun health(base: String): String? = try {
        val resp = request(base, "/healthz")
        if (resp.code == 200) null else "服务器返回 ${resp.code}"
    } catch (e: Exception) {
        e.message ?: "连接失败"
    }

    fun login(base: String, username: String, password: String): LoginResult = try {
        val resp = request(
            base, "/login",
            mapOf("username" to username, "password" to password, "rd" to "/"),
        )
        when {
            resp.code == 303 -> LoginResult.Success
            resp.code == 200 -> {
                val otp = PortalParser.otpToken(resp.body)
                when {
                    otp != null -> LoginResult.NeedOtp(otp)
                    else -> LoginResult.Failure(
                        PortalParser.error(resp.body) ?: "登录失败（${resp.code}）"
                    )
                }
            }
            else -> LoginResult.Failure("服务器返回 ${resp.code}")
        }
    } catch (e: Exception) {
        LoginResult.Failure(e.message ?: "网络错误")
    }

    fun otp(base: String, token: String, code: String): LoginResult = try {
        val resp = request(
            base, "/login/otp",
            mapOf("token" to token, "code" to code, "rd" to "/"),
        )
        when {
            resp.code == 303 -> LoginResult.Success
            resp.code == 200 -> {
                val newToken = PortalParser.otpToken(resp.body) ?: token
                val msg = PortalParser.error(resp.body)
                if (msg != null) {
                    LoginResult.Failure(msg)
                } else {
                    LoginResult.NeedOtp(newToken)
                }
            }
            else -> LoginResult.Failure("服务器返回 ${resp.code}")
        }
    } catch (e: Exception) {
        LoginResult.Failure(e.message ?: "网络错误")
    }

    fun apps(base: String): AppsResult = try {
        val resp = request(base, "/")
        when {
            resp.code == 200 -> AppsResult.Success(PortalParser.apps(resp.body))
            resp.code in 301..303 || resp.code == 307 || resp.code == 308 ->
                AppsResult.SessionExpired
            else -> AppsResult.Failure("服务器返回 ${resp.code}")
        }
    } catch (e: Exception) {
        AppsResult.Failure(e.message ?: "网络错误")
    }

    fun logout(base: String) {
        try {
            request(base, "/logout", emptyMap())
        } catch (e: Exception) {
            // 登出失败也继续清理本地会话
        }
    }
}
