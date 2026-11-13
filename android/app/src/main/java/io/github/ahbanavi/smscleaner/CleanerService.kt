package io.github.ahbanavi.smscleaner

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.os.Build
import android.os.IBinder

/**
 * Keeps the Go server alive while the app is open or a clean is running, so a
 * clean finishes even if you switch apps. Stops itself about a minute after
 * you leave the app with nothing in progress.
 */
class CleanerService : Service() {
    @Volatile private var stopping = false
    private var watcher: Thread? = null

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onCreate() {
        super.onCreate()
        val nm = getSystemService(NotificationManager::class.java)
        nm.createNotificationChannel(
            NotificationChannel(CHANNEL, getString(R.string.channel_name), NotificationManager.IMPORTANCE_LOW),
        )
        val n = notification(busy = false)
        if (Build.VERSION.SDK_INT >= 34) {
            startForeground(NOTIF_ID, n, ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE)
        } else {
            startForeground(NOTIF_ID, n)
        }
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == ACTION_STOP) {
            stopping = true
            stopSelf()
            return START_NOT_STICKY
        }
        if (watcher == null) {
            watcher = Thread(::watch, "smsc-watch").apply { start() }
        }
        return START_NOT_STICKY
    }

    private fun watch() {
        if (!GoServer.start(this)) {
            ready = false
            stopSelf()
            return
        }
        ready = true
        var idleChecks = 0
        var wasBusy = false
        while (!stopping) {
            val busy = GoServer.busy()
            if (busy != wasBusy) {
                getSystemService(NotificationManager::class.java).notify(NOTIF_ID, notification(busy))
                wasBusy = busy
            }
            idleChecks = if (!MainActivity.visible && !busy) idleChecks + 1 else 0
            if (idleChecks >= 4 || !GoServer.running) break // ~1 minute idle in the background
            try {
                Thread.sleep(15_000)
            } catch (_: InterruptedException) {
                break
            }
        }
        stopSelf()
    }

    override fun onDestroy() {
        stopping = true
        ready = false
        watcher?.interrupt()
        Thread { GoServer.stop() }.start()
        super.onDestroy()
    }

    private fun notification(busy: Boolean): Notification {
        val open = PendingIntent.getActivity(
            this, 0, Intent(this, MainActivity::class.java), PendingIntent.FLAG_IMMUTABLE,
        )
        val stop = PendingIntent.getService(
            this, 1, Intent(this, CleanerService::class.java).setAction(ACTION_STOP), PendingIntent.FLAG_IMMUTABLE,
        )
        return Notification.Builder(this, CHANNEL)
            .setSmallIcon(R.drawable.ic_stat)
            .setContentTitle(getString(R.string.app_name))
            .setContentText(getString(if (busy) R.string.notif_busy else R.string.notif_idle))
            .setContentIntent(open)
            .setOngoing(true)
            .addAction(Notification.Action.Builder(null, getString(R.string.stop), stop).build())
            .build()
    }

    companion object {
        private const val CHANNEL = "running"
        private const val NOTIF_ID = 1
        private const val ACTION_STOP = "stop"

        /** True once the server answers. */
        @Volatile var ready = false
            private set

        fun start(ctx: Context) {
            ctx.startForegroundService(Intent(ctx, CleanerService::class.java))
        }
    }
}
