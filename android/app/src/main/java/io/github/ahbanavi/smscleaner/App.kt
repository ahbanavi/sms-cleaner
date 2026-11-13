package io.github.ahbanavi.smscleaner

import android.app.Application

class App : Application() {
    override fun onCreate() {
        super.onCreate()
        KeepAliveWorker.schedule(this)
        CleanWorker.schedule(this)
    }
}
