package io.github.ahbanavi.smscleaner

import android.content.Context
import androidx.work.Constraints
import androidx.work.ExistingPeriodicWorkPolicy
import androidx.work.NetworkType
import androidx.work.PeriodicWorkRequestBuilder
import androidx.work.WorkManager
import androidx.work.Worker
import androidx.work.WorkerParameters
import java.util.concurrent.TimeUnit

/**
 * Google signs out Messages for web sessions that stay unused for about two
 * weeks. Every three days this connects once so the cleaner stays paired even
 * if you only open the app every other week.
 */
class KeepAliveWorker(ctx: Context, params: WorkerParameters) : Worker(ctx, params) {
    override fun doWork(): Result {
        if (!GoServer.paired(applicationContext)) return Result.success()
        return if (GoServer.ping(applicationContext) == 0) Result.success() else Result.retry()
    }

    companion object {
        fun schedule(ctx: Context) {
            val req = PeriodicWorkRequestBuilder<KeepAliveWorker>(3, TimeUnit.DAYS)
                .setConstraints(Constraints.Builder().setRequiredNetworkType(NetworkType.CONNECTED).build())
                .setBackoffCriteria(androidx.work.BackoffPolicy.EXPONENTIAL, 30, TimeUnit.MINUTES)
                .build()
            WorkManager.getInstance(ctx).enqueueUniquePeriodicWork("keepalive", ExistingPeriodicWorkPolicy.KEEP, req)
        }
    }
}
