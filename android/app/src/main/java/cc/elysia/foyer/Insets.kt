package cc.elysia.foyer

import android.app.Activity
import android.graphics.Color
import android.graphics.Insets
import android.os.Build
import android.view.View
import android.view.WindowInsets
import android.view.WindowInsetsController

/**
 * 开启边到边绘制：系统栏透明，并把状态栏/导航栏的图标改成深色，
 * 让栏位区域显示页面自己的背景色，而不是默认的黑色。
 */
@Suppress("DEPRECATION")
fun Activity.enableEdgeToEdge() {
    // 先拿到 DecorView，避免它在 insetsController 为 null 时还没创建。
    val decor = window.decorView
    window.statusBarColor = Color.TRANSPARENT
    window.navigationBarColor = Color.TRANSPARENT
    if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
        // 三键导航时系统会加一层半透明遮罩，关掉才能露出页面背景。
        window.isNavigationBarContrastEnforced = false
    }
    if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
        window.setDecorFitsSystemWindows(false)
        val lightBars = WindowInsetsController.APPEARANCE_LIGHT_STATUS_BARS or
            WindowInsetsController.APPEARANCE_LIGHT_NAVIGATION_BARS
        window.insetsController?.setSystemBarsAppearance(lightBars, lightBars)
    } else {
        decor.systemUiVisibility = (
            View.SYSTEM_UI_FLAG_LAYOUT_FULLSCREEN
                or View.SYSTEM_UI_FLAG_LAYOUT_HIDE_NAVIGATION
                or View.SYSTEM_UI_FLAG_LAYOUT_STABLE
                or View.SYSTEM_UI_FLAG_LIGHT_STATUS_BAR
                or View.SYSTEM_UI_FLAG_LIGHT_NAVIGATION_BAR
            )
    }
}

/**
 * 把系统栏高度叠加到视图原有的内边距上。
 *
 * 这样状态栏和导航栏区域露出的是这个视图自己的背景色，
 * 不会出现突兀的黑条，也不会盖住页面内容。
 */
fun View.applySystemBarInsets(includeTop: Boolean = true, includeBottom: Boolean = true) {
    val baseLeft = paddingLeft
    val baseTop = paddingTop
    val baseRight = paddingRight
    val baseBottom = paddingBottom

    setOnApplyWindowInsetsListener { view, insets ->
        var left = 0
        var top = 0
        var right = 0
        var bottom = 0
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
            val bars = insets.getInsets(
                WindowInsets.Type.systemBars() or WindowInsets.Type.displayCutout()
            )
            left = bars.left
            top = bars.top
            right = bars.right
            bottom = bars.bottom
        } else {
            @Suppress("DEPRECATION")
            left = insets.systemWindowInsetLeft
            @Suppress("DEPRECATION")
            top = insets.systemWindowInsetTop
            @Suppress("DEPRECATION")
            right = insets.systemWindowInsetRight
            @Suppress("DEPRECATION")
            bottom = insets.systemWindowInsetBottom
        }
        view.setPadding(
            baseLeft + left,
            baseTop + if (includeTop) top else 0,
            baseRight + right,
            baseBottom + if (includeBottom) bottom else 0,
        )
        // 系统栏高度已经变成内边距，这里把它们消费掉，不再往子视图传。
        // 否则 WebView 会把刘海安全区再报给网页，用了 env(safe-area-inset-top)
        // 的页面会在顶部再留一段空白，表现为状态栏下面多出一条色带。
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
            WindowInsets.Builder(insets)
                .setInsets(WindowInsets.Type.systemBars(), Insets.NONE)
                .setInsets(WindowInsets.Type.displayCutout(), Insets.NONE)
                .build()
        } else {
            @Suppress("DEPRECATION")
            insets.consumeSystemWindowInsets()
        }
    }
    requestApplyInsets()
}

/** 按背景深浅切换系统栏图标：深色背景用浅色图标，浅色背景用深色图标。 */
@Suppress("DEPRECATION")
fun Activity.setSystemBarsIcons(darkBackground: Boolean) {
    if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
        val lightBars = WindowInsetsController.APPEARANCE_LIGHT_STATUS_BARS or
            WindowInsetsController.APPEARANCE_LIGHT_NAVIGATION_BARS
        window.insetsController?.setSystemBarsAppearance(
            if (darkBackground) 0 else lightBars,
            lightBars,
        )
    } else {
        val lightFlags = View.SYSTEM_UI_FLAG_LIGHT_STATUS_BAR or
            View.SYSTEM_UI_FLAG_LIGHT_NAVIGATION_BAR
        val flags = window.decorView.systemUiVisibility
        window.decorView.systemUiVisibility =
            if (darkBackground) flags and lightFlags.inv() else flags or lightFlags
    }
}
