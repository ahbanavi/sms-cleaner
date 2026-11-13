package io.github.ahbanavi.smscleaner

import android.content.Context
import android.content.pm.ApplicationInfo
import android.net.ConnectivityManager
import android.util.Base64
import android.util.Log
import java.io.File
import java.net.HttpURLConnection
import java.net.ServerSocket
import java.net.URL
import java.security.SecureRandom
import java.util.TimeZone
import java.util.concurrent.TimeUnit
import java.util.concurrent.locks.ReentrantLock
import kotlin.concurrent.withLock

/**
 * Runs the sms-cleaner Go binary (packaged as libsmscleaner.so) as a child
 * process. The server listens on 127.0.0.1 behind a password that changes on
 * every start, so other apps on the phone can't read your messages through it.
 */
object GoServer {
    private const val TAG = "smsc"
    const val USER = "app"

    /** Only one process may use the Google Messages session at a time. */
    private val sessionLock = ReentrantLock()
    @Volatile private var process: Process? = null
    @Volatile var port = 0
        private set
    @Volatile var password = ""
        private set

    fun dataDir(ctx: Context) = File(ctx.filesDir, "data").apply { mkdirs() }
    fun logFile(ctx: Context) = File(ctx.filesDir, "smsc.log")
    fun paired(ctx: Context) = File(dataDir(ctx), "session.json").exists()
    val running get() = process?.isAlive == true
    val url get() = "http://127.0.0.1:$port/"

    private fun binary(ctx: Context) = File(ctx.applicationInfo.nativeLibraryDir, "libsmscleaner.so")

    private fun command(ctx: Context, vararg args: String): ProcessBuilder {
        val log = logFile(ctx)
        if (log.length() > 1_000_000) log.delete()
        return ProcessBuilder(binary(ctx).absolutePath, *args).apply {
            environment().apply {
                put("SMSC_DATA_DIR", dataDir(ctx).absolutePath)
                put("SMSC_DNS", dnsServers(ctx))
                put("TZ", TimeZone.getDefault().id)
                put("HOME", ctx.filesDir.absolutePath)
                put("TMPDIR", ctx.cacheDir.absolutePath)
            }
            redirectErrorStream(true)
            redirectOutput(ProcessBuilder.Redirect.appendTo(log))
        }
    }

    /** DNS servers of the active network (a VPN's when one is on). */
    private fun dnsServers(ctx: Context): String {
        val cm = ctx.getSystemService(ConnectivityManager::class.java) ?: return ""
        val lp = cm.getLinkProperties(cm.activeNetwork) ?: return ""
        return lp.dnsServers.mapNotNull { it.hostAddress }.joinToString(",")
    }

    /** Starts the server if needed and waits until it answers. Call off the main thread. */
    fun start(ctx: Context, timeoutMs: Long = 20_000): Boolean {
        sessionLock.withLock {
            if (!running) {
                port = choosePort(ctx)
                password = randomToken()
                val pb = command(ctx, "serve").apply {
                    environment().apply {
                        put("SMSC_LISTEN", "127.0.0.1:$port")
                        put("SMSC_UI_USER", USER)
                        put("SMSC_UI_PASSWORD", password)
                    }
                }
                Log.i(TAG, "starting server on port $port")
                process = pb.start()
                // Debug builds leave the address for adb-driven testing (run-as only).
                val debuggable = ctx.applicationInfo.flags and ApplicationInfo.FLAG_DEBUGGABLE != 0
                val info = File(ctx.filesDir, "server.json")
                if (debuggable) info.writeText("{\"port\":$port,\"user\":\"$USER\",\"password\":\"$password\"}") else info.delete()
            }
        }
        val deadline = System.currentTimeMillis() + timeoutMs
        while (System.currentTimeMillis() < deadline) {
            if (!running) return false
            if (get("healthz", auth = false) != null) return true
            Thread.sleep(250)
        }
        return false
    }

    fun stop() {
        sessionLock.withLock {
            val p = process ?: return
            Log.i(TAG, "stopping server")
            p.destroy() // SIGTERM: the server shuts down cleanly
            if (!p.waitFor(5, TimeUnit.SECONDS)) p.destroyForcibly()
            process = null
        }
    }

    /** True while a clean, preview or pairing is in progress. */
    fun busy(): Boolean {
        val body = get("api/status") ?: return false
        return body.contains("\"running\":true") ||
            body.contains("\"status\":\"waiting\"") || body.contains("\"status\":\"confirm\"")
    }

    /** Connects to the phone once to keep the Messages for web session alive. Returns the exit code. */
    fun ping(ctx: Context): Int {
        if (!sessionLock.tryLock(3, TimeUnit.MINUTES)) return -1
        try {
            if (running) return 0 // the server keeps the session alive itself
            val p = command(ctx, "ping").start()
            if (!p.waitFor(3, TimeUnit.MINUTES)) {
                p.destroyForcibly()
                return -1
            }
            return p.exitValue()
        } finally {
            sessionLock.unlock()
        }
    }

    /**
     * Runs a scheduled clean if one is due (the `tick` command) and returns its
     * JSON report, or null when the app's own server is running (it schedules
     * cleans itself) or the run did not finish in time.
     */
    fun tick(ctx: Context): String? {
        if (!sessionLock.tryLock(5, TimeUnit.MINUTES)) return null
        try {
            if (running) return null
            val pb = command(ctx, "tick").apply {
                redirectErrorStream(false)
                redirectOutput(ProcessBuilder.Redirect.PIPE)
                redirectError(ProcessBuilder.Redirect.appendTo(logFile(ctx)))
            }
            val p = pb.start()
            var out = ""
            val reader = Thread { out = p.inputStream.bufferedReader().readText() }.apply { start() }
            if (!p.waitFor(9, TimeUnit.MINUTES)) {
                p.destroyForcibly()
                return null
            }
            reader.join(2000)
            return out
        } finally {
            sessionLock.unlock()
        }
    }

    fun lastLog(ctx: Context, lines: Int = 15): String =
        runCatching { logFile(ctx).readLines().takeLast(lines).joinToString("\n") }.getOrDefault("")

    private fun get(path: String, auth: Boolean = true): String? = runCatching {
        val c = URL(url + path).openConnection() as HttpURLConnection
        c.connectTimeout = 1000
        c.readTimeout = 3000
        if (auth) {
            val token = Base64.encodeToString("$USER:$password".toByteArray(), Base64.NO_WRAP)
            c.setRequestProperty("Authorization", "Basic $token")
        }
        try {
            if (c.responseCode == 200) c.inputStream.bufferedReader().readText() else null
        } finally {
            c.disconnect()
        }
    }.getOrNull()

    /** A fixed port per install keeps the web UI's saved preferences between starts. */
    private fun choosePort(ctx: Context): Int {
        val prefs = ctx.getSharedPreferences("server", Context.MODE_PRIVATE)
        val saved = prefs.getInt("port", 0)
        if (saved != 0 && portFree(saved)) return saved
        val fresh = ServerSocket(0).use { it.localPort }
        prefs.edit().putInt("port", fresh).apply()
        return fresh
    }

    private fun portFree(p: Int) = runCatching { ServerSocket(p).close() }.isSuccess

    private fun randomToken(): String {
        val b = ByteArray(24)
        SecureRandom().nextBytes(b)
        return Base64.encodeToString(b, Base64.NO_WRAP or Base64.URL_SAFE or Base64.NO_PADDING)
    }
}
