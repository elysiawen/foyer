package cc.elysia.foyer

import android.Manifest
import android.annotation.SuppressLint
import android.app.Activity
import android.app.DownloadManager
import android.content.ActivityNotFoundException
import android.content.Intent
import android.content.pm.PackageManager
import android.graphics.Bitmap
import android.graphics.Color
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.os.Environment
import android.os.Message
import android.view.Gravity
import android.view.MotionEvent
import android.view.View
import android.view.ViewConfiguration
import android.webkit.CookieManager
import android.webkit.URLUtil
import android.webkit.ValueCallback
import android.webkit.WebChromeClient
import android.webkit.WebResourceRequest
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.FrameLayout
import android.widget.ProgressBar
import android.widget.TextView
import android.widget.Toast
import kotlin.math.abs

/** 在共享 Foyer 会话的 WebView 中打开内网应用。 */
class WebAppActivity : Activity() {

    companion object {
        const val EXTRA_URL = "url"
        const val EXTRA_NAME = "name"
        private const val REQUEST_WRITE_STORAGE = 20
        private const val REQUEST_FILE_CHOOSER = 30

        /** 取页面顶部第一个不透明元素的背景色。 */
        private val BAR_COLOR_SCRIPT = """
            (function () {
              function bg(el) {
                while (el) {
                  var c = getComputedStyle(el).backgroundColor;
                  if (c && c !== 'rgba(0, 0, 0, 0)' && c !== 'transparent') return c;
                  el = el.parentElement;
                }
                return '';
              }
              var el = document.elementFromPoint(window.innerWidth / 2, 1) || document.body;
              return bg(el);
            })()
        """.trimIndent()
    }

    private lateinit var webView: WebView
    private lateinit var progress: ProgressBar
    private lateinit var base: String
    private var returningToLogin = false
    private var pendingDownload: (() -> Unit)? = null
    private var filePathCallback: ValueCallback<Array<Uri>>? = null

    @SuppressLint("SetJavaScriptEnabled")
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_web)
        enableEdgeToEdge()
        findViewById<View>(R.id.web_root).applySystemBarInsets()

        base = ConfigStore(this).baseUrl.orEmpty()
        val target = intent.getStringExtra(EXTRA_URL)
        if (base.isEmpty() || target.isNullOrEmpty()) {
            finish()
            return
        }

        setupDraggableExit()
        progress = findViewById(R.id.web_progress)
        webView = findViewById(R.id.web_view)

        webView.settings.apply {
            javaScriptEnabled = true
            domStorageEnabled = true
            mediaPlaybackRequiresUserGesture = false
            loadWithOverviewMode = true
            useWideViewPort = true
            builtInZoomControls = true
            displayZoomControls = false
        }

        CookieManager.getInstance().apply {
            setAcceptCookie(true)
            setAcceptThirdPartyCookies(webView, true)
        }

        webView.webViewClient = object : WebViewClient() {
            override fun shouldOverrideUrlLoading(
                view: WebView,
                request: WebResourceRequest,
            ): Boolean {
                val url = request.url.toString()
                if (isPortalLogin(url)) {
                    returnToLogin()
                    return true
                }
                return false
            }

            override fun onPageStarted(view: WebView, url: String, favicon: Bitmap?) {
                if (isPortalLogin(url)) {
                    view.stopLoading()
                    returnToLogin()
                    return
                }
                progress.visibility = View.VISIBLE
            }

            override fun onPageFinished(view: WebView, url: String) {
                progress.visibility = View.GONE
                syncBarsWithPage()
            }
        }

        webView.webChromeClient = object : WebChromeClient() {
            override fun onProgressChanged(view: WebView, newProgress: Int) {
                progress.progress = newProgress
                if (newProgress == 100) progress.visibility = View.GONE
            }

            override fun onCreateWindow(
                view: WebView,
                isDialog: Boolean,
                isUserGesture: Boolean,
                resultMsg: Message,
            ): Boolean {
                // target=_blank 不弹新窗口，直接复用当前 WebView。
                val transport = resultMsg.obj as? WebView.WebViewTransport ?: return false
                transport.webView = webView
                resultMsg.sendToTarget()
                return true
            }

            override fun onShowFileChooser(
                view: WebView,
                callback: ValueCallback<Array<Uri>>,
                params: FileChooserParams,
            ): Boolean {
                // 网页里的 <input type=file> 必须由宿主拉起系统选择器，
                // 否则用户点了上传按钮没有任何反应。
                filePathCallback?.onReceiveValue(null)
                filePathCallback = callback
                return try {
                    startActivityForResult(params.createIntent(), REQUEST_FILE_CHOOSER)
                    true
                } catch (e: ActivityNotFoundException) {
                    filePathCallback = null
                    false
                }
            }
        }

        webView.setDownloadListener { url, userAgent, contentDisposition, mimeType, _ ->
            prepareDownload(url, userAgent, contentDisposition, mimeType)
        }

        if (savedInstanceState == null) {
            webView.loadUrl(target)
        } else {
            webView.restoreState(savedInstanceState)
        }
    }

    private fun isPortalLogin(url: String): Boolean {
        val login = "$base/login"
        return url == login || url.startsWith("$login?") || url.startsWith("$login#")
    }

    /** 右上角的关闭按钮可以拖到屏幕任意位置，轻点则退出应用。 */
    private fun setupDraggableExit() {
        val exit = findViewById<TextView>(R.id.web_exit)
        val root = findViewById<View>(R.id.web_root)
        val slop = ViewConfiguration.get(this).scaledTouchSlop
        var downX = 0f
        var downY = 0f
        var startLeft = 0
        var startTop = 0
        var dragging = false

        exit.setOnClickListener { finish() }
        exit.setOnTouchListener { v, event ->
            val lp = v.layoutParams as FrameLayout.LayoutParams
            when (event.actionMasked) {
                MotionEvent.ACTION_DOWN -> {
                    downX = event.rawX
                    downY = event.rawY
                    startLeft = v.left - root.paddingLeft
                    startTop = v.top - root.paddingTop
                    dragging = false
                    true
                }

                MotionEvent.ACTION_MOVE -> {
                    val dx = (event.rawX - downX).toInt()
                    val dy = (event.rawY - downY).toInt()
                    if (!dragging && (abs(dx) > slop || abs(dy) > slop)) {
                        dragging = true
                        lp.gravity = Gravity.TOP or Gravity.START
                    }
                    if (dragging) {
                        val maxLeft = (root.width - root.paddingLeft - root.paddingRight - v.width)
                            .coerceAtLeast(0)
                        val maxTop = (root.height - root.paddingTop - root.paddingBottom - v.height)
                            .coerceAtLeast(0)
                        lp.leftMargin = (startLeft + dx).coerceIn(0, maxLeft)
                        lp.topMargin = (startTop + dy).coerceIn(0, maxTop)
                        v.layoutParams = lp
                    }
                    true
                }

                MotionEvent.ACTION_UP, MotionEvent.ACTION_CANCEL -> {
                    if (!dragging) v.performClick()
                    true
                }

                else -> false
            }
        }
    }

    /**
     * 取网页顶部实际背景色，给状态栏和导航条区域上色，
     * 并按背景深浅切换图标颜色，避免深色页面配一条白边。
     */
    private fun syncBarsWithPage() {
        webView.evaluateJavascript(BAR_COLOR_SCRIPT) { raw ->
            val color = parseCssColor(raw) ?: return@evaluateJavascript
            val luma = 0.299 * Color.red(color) +
                0.587 * Color.green(color) +
                0.114 * Color.blue(color)
            webView.setBackgroundColor(color)
            findViewById<View>(R.id.web_root).setBackgroundColor(color)
            setSystemBarsIcons(darkBackground = luma < 140)
        }
    }

    private fun parseCssColor(raw: String?): Int? {
        val match = Regex("""rgba?\(\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)""")
            .find(raw ?: return null) ?: return null
        return Color.rgb(
            match.groupValues[1].toInt(),
            match.groupValues[2].toInt(),
            match.groupValues[3].toInt(),
        )
    }

    private fun returnToLogin() {
        if (returningToLogin) return
        returningToLogin = true
        val intent = Intent(this, LoginActivity::class.java).apply {
            flags = Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TASK
        }
        startActivity(intent)
        finish()
    }

    private fun prepareDownload(
        url: String,
        userAgent: String,
        contentDisposition: String?,
        mimeType: String?,
    ) {
        if (Build.VERSION.SDK_INT <= Build.VERSION_CODES.P &&
            checkSelfPermission(Manifest.permission.WRITE_EXTERNAL_STORAGE) !=
            PackageManager.PERMISSION_GRANTED
        ) {
            pendingDownload = {
                startDownload(url, userAgent, contentDisposition, mimeType)
            }
            requestPermissions(
                arrayOf(Manifest.permission.WRITE_EXTERNAL_STORAGE),
                REQUEST_WRITE_STORAGE,
            )
            return
        }
        startDownload(url, userAgent, contentDisposition, mimeType)
    }

    private fun startDownload(
        url: String,
        userAgent: String,
        contentDisposition: String?,
        mimeType: String?,
    ) {
        try {
            val fileName = URLUtil.guessFileName(url, contentDisposition, mimeType)
            val request = DownloadManager.Request(Uri.parse(url)).apply {
                setTitle(fileName)
                setDescription("正在下载")
                setNotificationVisibility(
                    DownloadManager.Request.VISIBILITY_VISIBLE_NOTIFY_COMPLETED
                )
                setDestinationInExternalPublicDir(Environment.DIRECTORY_DOWNLOADS, fileName)
                addRequestHeader("User-Agent", userAgent)
                CookieManager.getInstance().getCookie(url)?.let {
                    addRequestHeader("Cookie", it)
                }
                if (!mimeType.isNullOrEmpty()) setMimeType(mimeType)
            }
            getSystemService(DownloadManager::class.java).enqueue(request)
            Toast.makeText(this, "已开始下载", Toast.LENGTH_SHORT).show()
        } catch (e: ActivityNotFoundException) {
            Toast.makeText(this, "没有可用的下载管理器", Toast.LENGTH_LONG).show()
        } catch (e: IllegalArgumentException) {
            Toast.makeText(this, "无法下载此文件", Toast.LENGTH_LONG).show()
        }
    }

    override fun onRequestPermissionsResult(
        requestCode: Int,
        permissions: Array<out String>,
        grantResults: IntArray,
    ) {
        super.onRequestPermissionsResult(requestCode, permissions, grantResults)
        if (requestCode != REQUEST_WRITE_STORAGE) return
        val task = pendingDownload
        pendingDownload = null
        if (grantResults.firstOrNull() == PackageManager.PERMISSION_GRANTED) {
            task?.invoke()
        } else {
            Toast.makeText(this, "需要存储权限才能下载", Toast.LENGTH_LONG).show()
        }
    }

    @Deprecated("Deprecated in Java")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        if (requestCode == REQUEST_FILE_CHOOSER) {
            val callback = filePathCallback
            filePathCallback = null
            callback?.onReceiveValue(fileChooserResult(resultCode, data))
            return
        }
        super.onActivityResult(requestCode, resultCode, data)
    }

    private fun fileChooserResult(resultCode: Int, data: Intent?): Array<Uri>? {
        if (resultCode != RESULT_OK || data == null) return null
        data.clipData?.let { clip ->
            return Array(clip.itemCount) { clip.getItemAt(it).uri }
        }
        return data.data?.let { arrayOf(it) }
    }

    private fun handleBack() {
        if (webView.canGoBack()) webView.goBack() else finish()
    }

    @Deprecated("Deprecated in Java")
    override fun onBackPressed() = handleBack()

    override fun onSaveInstanceState(outState: Bundle) {
        webView.saveState(outState)
        super.onSaveInstanceState(outState)
    }

    override fun onPause() {
        webView.onPause()
        CookieManager.getInstance().flush()
        super.onPause()
    }

    override fun onResume() {
        super.onResume()
        webView.onResume()
    }

    override fun onDestroy() {
        if (::webView.isInitialized) webView.destroy()
        super.onDestroy()
    }
}
