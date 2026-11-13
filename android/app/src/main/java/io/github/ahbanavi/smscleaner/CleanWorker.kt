package io.github.ahbanavi.smscleaner

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import androidx.work.Constraints
import androidx.work.ExistingPeriodicWorkPolicy
import androidx.work.NetworkType
import androidx.work.PeriodicWorkRequestBuilder
import androidx.work.WorkManager
import androidx.work.Worker
import androidx.work.WorkerParameters
import org.json.JSONObject
import java.util.concurrent.TimeUnit

/**
 * Automatic cleaning while the app is closed. Every hour it asks the cleaner
 * whether a clean is due (Settings › Cleaning: the interval and whether runs
 * change the phone); if so the cleaner runs one, and a live clean that changed
 * something posts a summary notification.
 */
class CleanWorker(ctx: Context, params: WorkerParameters) : Worker(ctx, params) {
    override fun doWork(): Result {
        val app = applicationContext
        if (!GoServer.paired(app)) return Result.success()
        val out = GoServer.tick(app) ?: return Result.success()
        val report = runCatching { JSONObject(out) }.getOrNull() ?: return Result.success()
        if (!report.optBoolean("due")) return Result.success()
        val error = report.optString("error")
        val stats = report.optJSONObject("stats") ?: JSONObject()
        if (error.isNotEmpty()) {
            notify(app, "Automatic clean failed", error)
        } else if (!report.optBoolean("dry")) {
            val spam = stats.optInt("plan_block")
            val offers = stats.optInt("plan_delete") + stats.optInt("action_delete_message")
            val archived = stats.optInt("plan_archive")
            if (spam + offers + archived > 0) {
                notify(app, "SMS cleaned", "$spam spam blocked, $offers offers removed, $archived archived")
            }
        }
        return Result.success()
    }

    companion object {
        private const val CHANNEL = "cleans"

        fun schedule(ctx: Context) {
            val req = PeriodicWorkRequestBuilder<CleanWorker>(1, TimeUnit.HOURS)
                .setConstraints(Constraints.Builder().setRequiredNetworkType(NetworkType.CONNECTED).build())
                .build()
            WorkManager.getInstance(ctx).enqueueUniquePeriodicWork("clean", ExistingPeriodicWorkPolicy.KEEP, req)
        }

        private fun notify(ctx: Context, title: String, text: String) {
            val nm = ctx.getSystemService(NotificationManager::class.java)
            nm.createNotificationChannel(NotificationChannel(CHANNEL, "Automatic cleans", NotificationManager.IMPORTANCE_DEFAULT))
            val open = PendingIntent.getActivity(
                ctx, 0, Intent(ctx, MainActivity::class.java), PendingIntent.FLAG_IMMUTABLE,
            )
            val n = Notification.Builder(ctx, CHANNEL)
                .setSmallIcon(R.drawable.ic_stat)
                .setContentTitle(title)
                .setContentText(text)
                .setContentIntent(open)
                .setAutoCancel(true)
                .build()
            nm.notify(2, n)
        }
    }
}
