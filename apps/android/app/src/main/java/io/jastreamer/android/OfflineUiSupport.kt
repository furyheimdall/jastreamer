package io.jastreamer.android

import android.content.Context
import android.content.res.ColorStateList
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.graphics.Color
import android.graphics.drawable.GradientDrawable
import android.graphics.drawable.StateListDrawable
import android.view.Gravity
import android.widget.Button
import android.widget.ImageView
import java.io.File
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

internal object OfflinePalette {
    val background = Color.rgb(17, 19, 21)
    val panel = Color.rgb(27, 31, 34)
    val panelRaised = Color.rgb(37, 43, 47)
    val foreground = Color.rgb(244, 244, 236)
    val muted = Color.rgb(154, 163, 166)
    val accent = Color.rgb(200, 237, 178)
    val accentInk = Color.rgb(22, 33, 17)
    val border = Color.argb(30, 255, 255, 255)
    val error = Color.rgb(255, 155, 155)
}

internal object OfflinePlayerUi {
    fun configureIconButton(
        button: Button,
        iconResource: Int,
        label: String,
        primary: Boolean = false,
    ) {
        val density = button.resources.displayMetrics.density
        button.text = label
        button.tag = iconResource
        button.contentDescription = label
        button.textSize = 0f
        button.includeFontPadding = false
        button.isAllCaps = false
        button.gravity = Gravity.CENTER
        button.minWidth = (48 * density).toInt()
        button.minHeight = (48 * density).toInt()
        button.minimumWidth = (48 * density).toInt()
        button.minimumHeight = (48 * density).toInt()
        button.setPadding(
            (12 * density).toInt(),
            (12 * density).toInt(),
            (12 * density).toInt(),
            (12 * density).toInt(),
        )
        button.setCompoundDrawablesRelativeWithIntrinsicBounds(iconResource, 0, 0, 0)
        button.compoundDrawableTintList = ColorStateList(
            arrayOf(
                intArrayOf(-android.R.attr.state_enabled),
                intArrayOf(android.R.attr.state_selected),
                intArrayOf(),
            ),
            intArrayOf(
                withAlpha(if (primary) OfflinePalette.accentInk else OfflinePalette.muted, 0x66),
                OfflinePalette.accentInk,
                if (primary) OfflinePalette.accentInk else OfflinePalette.foreground,
            ),
        )
        button.setTextColor(Color.TRANSPARENT)
        button.background = iconBackground(button.context, primary)
    }

    fun styleArtwork(view: ImageView, cornerDp: Int = 10) {
        val density = view.resources.displayMetrics.density
        view.background = roundedBackground(
            OfflinePalette.panelRaised,
            cornerDp * density,
            OfflinePalette.border,
            density,
        )
        view.clipToOutline = true
    }

    fun cardBackground(context: Context, radiusDp: Int = 14): GradientDrawable {
        val density = context.resources.displayMetrics.density
        return roundedBackground(
            OfflinePalette.panel,
            radiusDp * density,
            OfflinePalette.border,
            density,
        )
    }

    private fun iconBackground(context: Context, primary: Boolean): StateListDrawable {
        val density = context.resources.displayMetrics.density
        fun shape(color: Int, stroke: Int = OfflinePalette.border) =
            roundedBackground(color, 999f * density, stroke, density)
        return StateListDrawable().apply {
            addState(
                intArrayOf(-android.R.attr.state_enabled),
                shape(if (primary) withAlpha(OfflinePalette.accent, 0x55) else withAlpha(OfflinePalette.panelRaised, 0x66)),
            )
            addState(
                intArrayOf(android.R.attr.state_pressed),
                shape(if (primary) Color.rgb(218, 246, 202) else withAlpha(OfflinePalette.foreground, 0x1F)),
            )
            addState(
                intArrayOf(android.R.attr.state_selected),
                shape(OfflinePalette.accent, OfflinePalette.accent),
            )
            addState(
                intArrayOf(),
                shape(if (primary) OfflinePalette.accent else withAlpha(OfflinePalette.foreground, 0x0A)),
            )
        }
    }

    private fun roundedBackground(color: Int, radius: Float, stroke: Int, density: Float) =
        GradientDrawable().apply {
            shape = GradientDrawable.RECTANGLE
            setColor(color)
            cornerRadius = radius
            setStroke(density.coerceAtLeast(1f).toInt(), stroke)
        }

    private fun withAlpha(color: Int, alpha: Int) = Color.argb(
        alpha,
        Color.red(color),
        Color.green(color),
        Color.blue(color),
    )
}

/** Bounded, sampled local artwork decoding; no remote lookup is ever attempted. */
internal object OfflineArtworkLoader {
    private const val MAX_CACHE_BYTES = 8 * 1024 * 1024
    private val cache = object : android.util.LruCache<String, Bitmap>(MAX_CACHE_BYTES) {
        override fun sizeOf(key: String, value: Bitmap): Int = value.allocationByteCount
    }

    fun load(scope: CoroutineScope, view: ImageView, path: String?, targetPixels: Int) {
        showPlaceholder(view)
        view.tag = path
        if (path.isNullOrBlank()) return
        cache.get(path)?.let {
            showArtwork(view, it)
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
            if (view.tag == path && bitmap != null) showArtwork(view, bitmap)
        }
    }

    private fun showPlaceholder(view: ImageView) {
        val padding = (view.resources.displayMetrics.density * 12).toInt()
        view.setPadding(padding, padding, padding, padding)
        view.scaleType = ImageView.ScaleType.CENTER_INSIDE
        view.setImageResource(R.drawable.ic_offline_music)
    }

    private fun showArtwork(view: ImageView, bitmap: Bitmap) {
        view.setPadding(0, 0, 0, 0)
        view.scaleType = ImageView.ScaleType.CENTER_CROP
        view.setImageBitmap(bitmap)
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
