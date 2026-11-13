package io.github.ahbanavi.smscleaner

import android.Manifest
import android.annotation.SuppressLint
import android.app.Activity
import android.content.Intent
import android.content.pm.PackageManager
import android.content.res.Configuration
import android.graphics.Typeface
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.view.Gravity
import android.view.View
import android.view.WindowInsets
import android.view.WindowInsetsController
import android.webkit.HttpAuthHandler
import android.webkit.ValueCallback
import android.webkit.WebChromeClient
import android.webkit.WebResourceRequest
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.Button
import android.widget.FrameLayout
import android.widget.LinearLayout
import android.widget.TextView
import android.window.OnBackInvokedDispatcher

/** Shows the cleaner's web UI, served by the Go process on this phone. */
class MainActivity : Activity() {
    private lateinit var root: FrameLayout
    private lateinit var web: WebView
    private lateinit var status: LinearLayout
    private lateinit var statusText: TextView
    private lateinit var retry: Button
    private var loadedPort = 0
    private var fileCallback: ValueCallback<Array<Uri>>? = null

    @SuppressLint("SetJavaScriptEnabled")
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        edgeToEdge()

        web = WebView(this).apply {
            visibility = View.INVISIBLE
            settings.javaScriptEnabled = true
            settings.domStorageEnabled = true
            webViewClient = Client()
            webChromeClient = Chrome()
        }
        statusText = TextView(this).apply {
            setText(R.string.starting)
            textSize = 16f
            gravity = Gravity.CENTER
            setTextColor(getColor(R.color.ink))
        }
        retry = Button(this).apply {
            text = "Try again"
            visibility = View.GONE
            setOnClickListener { startAndLoad() }
        }
        status = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            gravity = Gravity.CENTER
            setPadding(48, 48, 48, 48)
            addView(statusText)
            addView(retry, LinearLayout.LayoutParams(-2, -2).apply { gravity = Gravity.CENTER_HORIZONTAL; topMargin = 32 })
        }
        root = FrameLayout(this).apply {
            addView(web, FrameLayout.LayoutParams(-1, -1))
            addView(status, FrameLayout.LayoutParams(-1, -1))
        }
        root.setOnApplyWindowInsetsListener { v, insets ->
            if (Build.VERSION.SDK_INT >= 30) {
                val i = insets.getInsets(WindowInsets.Type.systemBars() or WindowInsets.Type.ime())
                v.setPadding(i.left, i.top, i.right, i.bottom)
            }
            insets
        }
        setContentView(root)

        if (Build.VERSION.SDK_INT >= 33) {
            onBackInvokedDispatcher.registerOnBackInvokedCallback(OnBackInvokedDispatcher.PRIORITY_DEFAULT) { back() }
            if (checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED) {
                requestPermissions(arrayOf(Manifest.permission.POST_NOTIFICATIONS), 1)
            }
        }
    }

    override fun onStart() {
        super.onStart()
        visible = true
        // The service stops itself after you leave the app; bring it back.
        if (!GoServer.running || GoServer.port != loadedPort) startAndLoad()
    }

    override fun onStop() {
        visible = false
        super.onStop()
    }

    private fun startAndLoad() {
        statusText.setText(R.string.starting)
        retry.visibility = View.GONE
        status.visibility = View.VISIBLE
        CleanerService.start(this)
        Thread {
            val deadline = System.currentTimeMillis() + 30_000
            while (!CleanerService.ready && System.currentTimeMillis() < deadline) Thread.sleep(200)
            runOnUiThread {
                if (CleanerService.ready) {
                    loadedPort = GoServer.port
                    web.loadUrl(GoServer.url)
                    web.visibility = View.VISIBLE
                    status.visibility = View.GONE
                } else {
                    statusText.text = getString(R.string.start_failed) + "\n\n" + GoServer.lastLog(this)
                    statusText.typeface = Typeface.MONOSPACE
                    statusText.textSize = 12f
                    retry.visibility = View.VISIBLE
                }
            }
        }.start()
    }

    private fun back() {
        if (web.canGoBack()) web.goBack() else finish()
    }

    @Deprecated("Used below API 33")
    override fun onBackPressed() {
        if (web.canGoBack()) web.goBack() else @Suppress("DEPRECATION") super.onBackPressed()
    }

    private fun edgeToEdge() {
        if (Build.VERSION.SDK_INT < 30) return
        // Android 15+ always draws edge to edge; earlier versions need asking.
        if (Build.VERSION.SDK_INT < 35) @Suppress("DEPRECATION") window.setDecorFitsSystemWindows(false)
        val night = (resources.configuration.uiMode and Configuration.UI_MODE_NIGHT_MASK) == Configuration.UI_MODE_NIGHT_YES
        val light = WindowInsetsController.APPEARANCE_LIGHT_STATUS_BARS or WindowInsetsController.APPEARANCE_LIGHT_NAVIGATION_BARS
        window.insetsController?.setSystemBarsAppearance(if (night) 0 else light, light)
    }

    private inner class Client : WebViewClient() {
        override fun onReceivedHttpAuthRequest(view: WebView, handler: HttpAuthHandler, host: String, realm: String) {
            if (host == "127.0.0.1") handler.proceed(GoServer.USER, GoServer.password) else handler.cancel()
        }

        // Links in messages and instructions open in the browser, not inside the app.
        override fun shouldOverrideUrlLoading(view: WebView, request: WebResourceRequest): Boolean {
            if (request.url.host == "127.0.0.1") return false
            runCatching { startActivity(Intent(Intent.ACTION_VIEW, request.url)) }
            return true
        }
    }

    private inner class Chrome : WebChromeClient() {
        override fun onShowFileChooser(view: WebView, callback: ValueCallback<Array<Uri>>, params: FileChooserParams): Boolean {
            fileCallback?.onReceiveValue(null)
            fileCallback = callback
            return runCatching {
                @Suppress("DEPRECATION")
                startActivityForResult(params.createIntent(), PICK_FILE)
            }.onFailure { fileCallback = null }.isSuccess
        }
    }

    @Deprecated("Activity result API needs AndroidX; the platform call is enough here")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        if (requestCode == PICK_FILE) {
            fileCallback?.onReceiveValue(WebChromeClient.FileChooserParams.parseResult(resultCode, data))
            fileCallback = null
            return
        }
        @Suppress("DEPRECATION")
        super.onActivityResult(requestCode, resultCode, data)
    }

    companion object {
        private const val PICK_FILE = 7

        /** Whether the UI is on screen; the service stops itself when it isn't and nothing is running. */
        @Volatile var visible = false
    }
}
