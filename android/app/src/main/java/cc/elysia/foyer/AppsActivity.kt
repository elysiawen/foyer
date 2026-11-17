package cc.elysia.foyer

import android.app.Activity
import android.content.Intent
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.view.View
import android.widget.Button
import android.widget.ListView
import android.widget.TextView
import android.widget.Toast
import androidx.swiperefreshlayout.widget.SwipeRefreshLayout
import com.google.android.material.dialog.MaterialAlertDialogBuilder

/** 原生应用列表。 */
class AppsActivity : Activity() {

    private val main = Handler(Looper.getMainLooper())
    private lateinit var base: String
    private lateinit var swipe: SwipeRefreshLayout
    private lateinit var adapter: AppsAdapter
    private lateinit var empty: TextView

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_apps)
        enableEdgeToEdge()
        findViewById<View>(R.id.apps_header).applySystemBarInsets(includeBottom = false)
        findViewById<View>(R.id.apps_swipe).applySystemBarInsets(includeTop = false)

        base = ConfigStore(this).baseUrl ?: ""
        if (base.isEmpty()) {
            startActivity(Intent(this, ServerConfigActivity::class.java))
            finish()
            return
        }

        swipe = findViewById(R.id.apps_swipe)
        empty = findViewById(R.id.apps_empty)
        val list = findViewById<ListView>(R.id.apps_list)
        adapter = AppsAdapter(this)
        list.adapter = adapter
        list.emptyView = empty

        list.setOnItemClickListener { _, _, position, _ ->
            val app = adapter.getItem(position)
            val intent = Intent(this, WebAppActivity::class.java)
            intent.putExtra(WebAppActivity.EXTRA_URL, base + app.href)
            intent.putExtra(WebAppActivity.EXTRA_NAME, app.name)
            startActivity(intent)
        }

        swipe.setOnRefreshListener { loadApps() }
        findViewById<Button>(R.id.apps_logout).setOnClickListener { confirmLogout() }
        findViewById<Button>(R.id.apps_server).setOnClickListener { confirmChangeServer() }

        loadApps()
    }

    private fun loadApps() {
        swipe.isRefreshing = true
        Thread {
            val result = FoyerApi.apps(base)
            main.post {
                swipe.isRefreshing = false
                when (result) {
                    is FoyerApi.AppsResult.Success -> {
                        adapter.replace(result.apps)
                        empty.text = "还没有分配给你的应用"
                    }
                    is FoyerApi.AppsResult.SessionExpired -> {
                        startActivity(Intent(this, LoginActivity::class.java))
                        finish()
                    }
                    is FoyerApi.AppsResult.Failure ->
                        Toast.makeText(this, result.message, Toast.LENGTH_LONG).show()
                }
            }
        }.start()
    }

    private fun confirmLogout() {
        MaterialAlertDialogBuilder(this)
            .setMessage("确定退出登录吗？")
            .setPositiveButton("退出") { _, _ ->
                Thread { FoyerApi.logout(base) }.start()
                CookieSync.clearAll {
                    startActivity(Intent(this, LoginActivity::class.java))
                    finish()
                }
            }
            .setNegativeButton("取消", null)
            .show()
    }

    private fun confirmChangeServer() {
        MaterialAlertDialogBuilder(this)
            .setMessage("更换服务器会退出当前账号，继续吗？")
            .setPositiveButton("继续") { _, _ ->
                Thread { FoyerApi.logout(base) }.start()
                CookieSync.clearAll {
                    ConfigStore(this).clear()
                    startActivity(Intent(this, ServerConfigActivity::class.java))
                    finish()
                }
            }
            .setNegativeButton("取消", null)
            .show()
    }
}
