package io.jastreamer.android

import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.widget.ImageView
import java.io.File
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/** Bounded, sampled local artwork decoding; no remote lookup is ever attempted. */
internal object OfflineArtworkLoader {
    private const val MAX_CACHE_BYTES = 8 * 1024 * 1024
    private val cache = object : android.util.LruCache<String, Bitmap>(MAX_CACHE_BYTES) {
        override fun sizeOf(key: String, value: Bitmap): Int = value.allocationByteCount
    }

    fun load(scope: CoroutineScope, view: ImageView, path: String?, targetPixels: Int) {
        view.setImageResource(android.R.drawable.ic_media_play)
        view.tag = path
        if (path.isNullOrBlank()) return
        cache.get(path)?.let {
            view.setImageBitmap(it)
            return
        }
        scope.launch {
            val bitmap = withContext(Dispatchers.IO) {
                try {
                    decodeSampled(path, targetPixels)
                } catch (_: RuntimeException) {
                    null
                }
            }
            if (bitmap != null) cache.put(path, bitmap)
            if (view.tag == path && bitmap != null) view.setImageBitmap(bitmap)
        }
    }

    private fun decodeSampled(path: String, targetPixels: Int): Bitmap? {
        val file = File(path)
        if (!file.isFile || targetPixels <= 0) return null
        val bounds = BitmapFactory.Options().apply { inJustDecodeBounds = true }
        BitmapFactory.decodeFile(file.absolutePath, bounds)
        if (bounds.outWidth <= 0 || bounds.outHeight <= 0) return null
        var sample = 1
        while (bounds.outWidth / (sample * 2) >= targetPixels && bounds.outHeight / (sample * 2) >= targetPixels) {
            sample *= 2
        }
        return BitmapFactory.decodeFile(file.absolutePath, BitmapFactory.Options().apply { inSampleSize = sample })
    }
}
