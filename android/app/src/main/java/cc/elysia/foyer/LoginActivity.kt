package cc.elysia.foyer

import android.app.Activity
import android.content.Intent
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.text.InputType
import android.view.View
import android.widget.Button
import android.widget.EditText
import android.widget.FrameLayout
import android.widget.ProgressBar
import android.widget.TextView
import android.widget.Toast
import androidx.appcompat.app.AlertDialog
import com.google.android.material.dialog.MaterialAlertDialogBuilder

/** 原生登录页，支持 2FA 动态验证码。 */
class LoginActivity : Activity() {

    private val main = Handler(Looper.getMainLooper())
    private lateinit var base: String
    private lateinit var loginBtn: Button
    private lateinit var progress: ProgressBar
    private var otpDialog: AlertDialog? = null

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_login)
        enableEdgeToEdge()
        findViewById<View>(R.id.login_root).applySystemBarInsets()

        base = ConfigStore(this).baseUrl ?: ""
        if (base.isEmpty()) {
            startActivity(Intent(this, ServerConfigActivity::class.java))
            finish()
            return
        }

        val username = findViewById<EditText>(R.id.login_username)
        val password = findViewById<EditText>(R.id.login_password)
        loginBtn = findViewById(R.id.login_submit)
        progress = findViewById(R.id.login_progress)
        findViewById<TextView>(R.id.login_server).text = base

        loginBtn.setOnClickListener {
            val u = username.text.toString().trim()
            val p = password.text.toString()
            if (u.isEmpty() || p.isEmpty()) {
                toast("请输入用户名和密码")
                return@setOnClickListener
            }
            loading(true)
            Thread {
                val result = FoyerApi.login(base, u, p)
                main.post {
                    loading(false)
                    handleResult(result)
                }
            }.start()
        }
    }

    private fun handleResult(result: FoyerApi.LoginResult) {
        when (result) {
            is FoyerApi.LoginResult.Success -> {
                startActivity(Intent(this, AppsActivity::class.java))
                finish()
            }
            is FoyerApi.LoginResult.NeedOtp -> showOtpDialog(result.token)
            is FoyerApi.LoginResult.Failure -> toast(result.message)
        }
    }

    private fun showOtpDialog(token: String) {
        otpDialog?.dismiss()
        val input = EditText(this).apply {
            hint = "6 位动态验证码"
            inputType = InputType.TYPE_CLASS_NUMBER
            maxLines = 1
            setBackgroundResource(R.drawable.bg_input)
            val pad = (16 * resources.displayMetrics.density).toInt()
            setPadding(pad, pad, pad, pad)
        }
        val container = FrameLayout(this).apply {
            val pad = (24 * resources.displayMetrics.density).toInt()
            setPadding(pad, pad / 2, pad, 0)
            addView(input)
        }
        val dialog = MaterialAlertDialogBuilder(this)
            .setTitle("两步验证")
            .setMessage("账号已开启 2FA，请输入验证器里的 6 位验证码。")
            .setView(container)
            .setPositiveButton("验证", null)
            .setNegativeButton("取消", null)
            .create()
        dialog.setOnShowListener {
            dialog.getButton(AlertDialog.BUTTON_POSITIVE).setOnClickListener {
                val code = input.text.toString().trim()
                if (code.length != 6) {
                    toast("请输入 6 位验证码")
                    return@setOnClickListener
                }
                loading(true)
                Thread {
                    val result = FoyerApi.otp(base, token, code)
                    main.post {
                        loading(false)
                        if (result is FoyerApi.LoginResult.Success) {
                            dialog.dismiss()
                        }
                        handleResult(result)
                    }
                }.start()
            }
        }
        otpDialog = dialog
        dialog.show()
    }

    private fun loading(on: Boolean) {
        loginBtn.isEnabled = !on
        progress.visibility = if (on) View.VISIBLE else View.GONE
    }

    private fun toast(msg: String) =
        Toast.makeText(this, msg, Toast.LENGTH_LONG).show()
}
