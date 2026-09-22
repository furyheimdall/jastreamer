package io.jastreamer.android

import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import androidx.core.app.NotificationCompat
import androidx.work.CoroutineWorker
import androidx.work.ForegroundInfo
import androidx.work.WorkerParameters
import kotlinx.coroutines.CancellationException

/** Durable WorkManager job using Android's dataSync foreground-service lifecycle. */
class OfflineDownloadService(
    appContext: Context,
    parameters: WorkerParameters,
) : CoroutineWorker(appContext, parameters) {
    override suspend fun doWork(): Result {
        createChannel()
        setForeground(foregroundInfo(null))
        return try {
            val retry = OfflineDownloads.process(
                applicationContext,
                progress = { job ->
                    setProgressAsync(androidx.work.workDataOf(
                        "job_id" to job?.id.orEmpty(),
                        "received_bytes" to (job?.receivedBytes ?: 0L),
                        "total_bytes" to (job?.totalBytes ?: 0L),
                    ))
                    setForegroundAsync(foregroundInfo(job))
                },
                localOnly = inputData.getBoolean("local_only", false),
            )
            if (retry) Result.retry() else Result.success()
        } catch (cancelled: CancellationException) {
            throw cancelled
        } catch (_: Exception) {
            Result.retry()
        }
    }


    private fun createChannel() {
        val channel = NotificationChannel(
            CHANNEL_ID,
            applicationContext.getString(R.string.offline_download_notification_channel),
            NotificationManager.IMPORTANCE_LOW,
        ).apply {
            description = applicationContext.getString(R.string.offline_download_notification_description)
            setShowBadge(false)
        }
        applicationContext.getSystemService(NotificationManager::class.java).createNotificationChannel(channel)
    }

    private fun foregroundInfo(job: OfflineDownloadJob?): ForegroundInfo {
        val launch = Intent(applicationContext, MainActivity::class.java).apply {
            flags = Intent.FLAG_ACTIVITY_SINGLE_TOP or Intent.FLAG_ACTIVITY_CLEAR_TOP
        }
        val pendingIntent = PendingIntent.getActivity(
            applicationContext,
            0,
            launch,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )
        val title = job?.title?.takeIf(String::isNotBlank)
            ?: applicationContext.getString(R.string.offline_download_notification_title)
        val text = when (job?.status) {
            "preparing" -> applicationContext.getString(R.string.offline_download_status_preparing)
            "importing" -> applicationContext.getString(R.string.offline_download_status_importing)
            "waiting" -> job.errorMessage ?: applicationContext.getString(R.string.offline_download_status_waiting)
            else -> applicationContext.getString(R.string.offline_download_status_downloading)
        }
        val builder = NotificationCompat.Builder(applicationContext, CHANNEL_ID)
            .setSmallIcon(R.drawable.icon_foreground)
            .setContentTitle(title)
            .setContentText(text)
            .setContentIntent(pendingIntent)
            .setOngoing(true)
            .setOnlyAlertOnce(true)
            .setCategory(NotificationCompat.CATEGORY_PROGRESS)
            .setVisibility(NotificationCompat.VISIBILITY_PRIVATE)
        if (job != null && job.totalBytes in 1..Int.MAX_VALUE && job.receivedBytes >= 0) {
            builder.setProgress(job.totalBytes.toInt(), job.receivedBytes.coerceAtMost(job.totalBytes).toInt(), false)
        } else {
            builder.setProgress(0, 0, true)
        }
        return ForegroundInfo(
            NOTIFICATION_ID,
            builder.build(),
            ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC,
        )
    }

    companion object {
        private const val CHANNEL_ID = "offline_downloads"
        private const val NOTIFICATION_ID = 8_201
    }
}
