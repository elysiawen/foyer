package cc.elysia.foyer

import android.app.Activity
import android.content.Intent
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.view.View
import android.widget.Button
import android.widget.EditText
import android.widget.ProgressBar
import android.widget.Toast

/** 首次启动配置服务器地址。 */
class ServerConfigActivity : Activity() {

    private val main = Handler(Looper.getMainLooper())

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_server)
        enableEdgeToEdge()
        findViewById<View>(R.id.server_root).applySystemBarInsets()

        val input = findViewById<EditText>(R.id.server_input)
        val save = findViewById<Button>(R.id.server_save)
        val progress = findViewById<ProgressBar>(R.id.server_progress)

        input.setText(ConfigStore(this).baseUrl ?: "http://")

        save.setOnClickListener {
            val base = ConfigStore.normalize(input.text.toString())
            if (base == null) {
                Toast.makeText(this, "请输入合法的服务器地址", Toast.LENGTH_SHORT).show()
                return@setOnClickListener
            }
            save.isEnabled = false
            progress.visibility = View.VISIBLE
            Thread {
                val error = FoyerApi.health(base)
                main.post {
                    progress.visibility = View.GONE
                    save.isEnabled = true
                    if (error == null) {
                        ConfigStore(this).baseUrl = base
                        Toast.makeText(this, "已连接服务器", Toast.LENGTH_SHORT).show()
                        startActivity(Intent(this, LoginActivity::class.java))
                        finish()
                    } else {
                        Toast.makeText(this, "连不上服务器：$error", Toast.LENGTH_LONG).show()
                    }
                }
            }.start()
        }
    }
}
