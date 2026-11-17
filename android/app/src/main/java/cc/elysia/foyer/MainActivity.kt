package cc.elysia.foyer

import android.app.Activity
import android.content.Intent
import android.os.Bundle
import android.webkit.CookieManager

/** 启动分发：未配置服务器去配置页，否则进应用列表（会话失效会自行跳登录）。 */
class MainActivity : Activity() {

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        CookieManager.getInstance().setAcceptCookie(true)

        val store = ConfigStore(this)
        val target = if (store.baseUrl == null) {
            ServerConfigActivity::class.java
        } else {
            AppsActivity::class.java
        }
        startActivity(Intent(this, target))
        finish()
    }
}
