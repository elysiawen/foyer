package cc.elysia.foyer

import org.jsoup.Jsoup

/** 门户服务端渲染 HTML 的解析。 */
object PortalParser {

    data class AppItem(
        val name: String,
        val desc: String,
        val icon: String,
        val href: String,
        val status: Status,
    )

    enum class Status { UP, DOWN, UNKNOWN }

    /** 登录响应里若包含 2FA 挑战，返回 pending token。 */
    fun otpToken(html: String): String? =
        Jsoup.parse(html).selectFirst("#login-otp input[name=token]")?.attr("value")

    /** 服务端放在隐藏 .flash 里的错误/提示文案。 */
    fun error(html: String): String? =
        Jsoup.parse(html).selectFirst(".flash[data-kind=error]")?.attr("data-message")
            ?.takeIf { it.isNotBlank() }

    fun apps(html: String): List<AppItem> =
        Jsoup.parse(html).select("a.app-card").map { card ->
            val dot = card.selectFirst(".dot")
            val status = when {
                dot == null -> Status.UNKNOWN
                dot.hasClass("up") -> Status.UP
                dot.hasClass("down") -> Status.DOWN
                else -> Status.UNKNOWN
            }
            AppItem(
                name = card.selectFirst(".app-name")?.text().orEmpty(),
                desc = card.selectFirst(".app-desc")?.text().orEmpty(),
                icon = card.selectFirst(".app-icon")?.text().orEmpty().ifEmpty { "◆" },
                href = card.attr("href"),
                status = status,
            )
        }
}
