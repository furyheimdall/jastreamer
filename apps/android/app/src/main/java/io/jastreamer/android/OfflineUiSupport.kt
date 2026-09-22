package io.jastreamer.android

import android.content.Context
import android.content.res.ColorStateList
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.graphics.Color
import android.graphics.drawable.Drawable
import android.graphics.drawable.GradientDrawable
import android.graphics.drawable.RippleDrawable
import android.graphics.drawable.StateListDrawable
import android.view.Gravity
import android.widget.Button
import android.widget.CheckBox
import android.widget.EditText
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
    val border = Color.argb(18, 255, 255, 255)
    val focus = Color.rgb(215, 246, 199)
    val input = Color.rgb(15, 18, 20)
    val hint = Color.rgb(120, 128, 132)
    val error = Color.rgb(255, 155, 155)
}

internal object OfflineUi {
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
        setMinimumTarget(button, density)
        button.setPadding(
            (12 * density).toInt(),
            (12 * density).toInt(),
            (12 * density).toInt(),
            (12 * density).toInt(),
        )
        button.setCompoundDrawablesRelativeWithIntrinsicBounds(iconResource, 0, 0, 0)
        button.compoundDrawableTintList = iconColors(primary)
        button.setTextColor(Color.TRANSPARENT)
        button.backgroundTintList = null
        button.background = interactiveBackground(
            context = button.context,
            radiusDp = 999,
            primary = primary,
            selected = true,
            ghost = !primary,
        )
    }

    fun configureButton(
        button: Button,
        primary: Boolean = false,
        danger: Boolean = false,
    ) {
        val density = button.resources.displayMetrics.density
        button.isAllCaps = false
        button.includeFontPadding = false
        button.gravity = Gravity.CENTER
        setMinimumTarget(button, density)
        button.setPadding(
            (16 * density).toInt(),
            (10 * density).toInt(),
            (16 * density).toInt(),
            (10 * density).toInt(),
        )
        button.setTextColor(buttonColors(primary, danger))
        button.compoundDrawableTintList = buttonColors(primary, danger)
        button.backgroundTintList = null
        button.background = interactiveBackground(
            context = button.context,
            radiusDp = 11,
            primary = primary,
            danger = danger,
            selected = false,
        )
    }

    fun configureTab(button: Button, selected: Boolean) {
        val density = button.resources.displayMetrics.density
        button.isSelected = selected
        button.isAllCaps = false
        button.includeFontPadding = false
        button.gravity = Gravity.CENTER
        button.textSize = 14f
        setMinimumTarget(button, density)
        button.setPadding(
            (16 * density).toInt(),
            (9 * density).toInt(),
            (16 * density).toInt(),
            (9 * density).toInt(),
        )
        button.setTextColor(selectionColors())
        button.compoundDrawableTintList = selectionColors()
        button.backgroundTintList = null
        button.background = interactiveBackground(
            context = button.context,
            radiusDp = 999,
            primary = false,
            selected = true,
            ghost = true,
        )
    }

    fun configureNavigationButton(
        button: Button,
        iconResource: Int,
        label: String,
        selected: Boolean,
        vertical: Boolean,
    ) {
        val density = button.resources.displayMetrics.density
        button.text = label
        button.tag = iconResource
        button.contentDescription = label
        button.isSelected = selected
        button.isAllCaps = false
        button.includeFontPadding = false
        button.textSize = if (vertical) 11f else 14f
        button.gravity = Gravity.CENTER
        setMinimumTarget(button, density)
        val horizontalPadding = if (vertical) 6 else 13
        button.setPadding(
            (horizontalPadding * density).toInt(),
            (7 * density).toInt(),
            (horizontalPadding * density).toInt(),
            (7 * density).toInt(),
        )
        button.compoundDrawablePadding = ((if (vertical) 3 else 12) * density).toInt()
        if (vertical) {
            button.setCompoundDrawablesRelativeWithIntrinsicBounds(0, iconResource, 0, 0)
        } else {
            button.setCompoundDrawablesRelativeWithIntrinsicBounds(iconResource, 0, 0, 0)
        }
        button.setTextColor(selectionColors())
        button.compoundDrawableTintList = selectionColors()
        button.backgroundTintList = null
        button.background = interactiveBackground(
            context = button.context,
            radiusDp = 11,
            primary = false,
            selected = true,
            ghost = true,
        )
    }

    fun configureInput(input: EditText) {
        val density = input.resources.displayMetrics.density
        input.setTextColor(textColors(OfflinePalette.foreground))
        input.setHintTextColor(textColors(OfflinePalette.hint))
        input.highlightColor = withAlpha(OfflinePalette.accent, 0x55)
        input.textCursorDrawable?.mutate()?.setTint(OfflinePalette.accent)
        input.backgroundTintList = null
        input.background = inputBackground(input.context)
        input.minimumHeight = (48 * density).toInt()
        input.minHeight = (48 * density).toInt()
        input.setPadding(
            (13 * density).toInt(),
            (10 * density).toInt(),
            (13 * density).toInt(),
            (10 * density).toInt(),
        )
    }

    fun configureCheckBox(checkBox: CheckBox) {
        val density = checkBox.resources.displayMetrics.density
        checkBox.isAllCaps = false
        checkBox.gravity = Gravity.CENTER_VERTICAL
        checkBox.minimumHeight = (48 * density).toInt()
        checkBox.minHeight = (48 * density).toInt()
        checkBox.setTextColor(textColors(OfflinePalette.foreground))
        checkBox.buttonTintList = checkColors()
        checkBox.backgroundTintList = null
        checkBox.background = controlBackground(checkBox.context)
        checkBox.setPadding(
            checkBox.paddingLeft,
            checkBox.paddingTop,
            (8 * density).toInt(),
            checkBox.paddingBottom,
        )
    }

    fun rowBackground(context: Context, selected: Boolean = false, plain: Boolean = false): Drawable {
        val density = context.resources.displayMetrics.density
        val color = when {
            plain -> Color.TRANSPARENT
            selected -> withAlpha(OfflinePalette.accent, 0x17)
            else -> OfflinePalette.panel
        }
        val stroke = when {
            plain -> Color.TRANSPARENT
            selected -> withAlpha(OfflinePalette.accent, 0x33)
            else -> OfflinePalette.border
        }
        val content = StateListDrawable().apply {
            addState(
                intArrayOf(-android.R.attr.state_enabled),
                roundedBackground(
                    withAlpha(color, Color.alpha(color) / 2),
                    11 * density,
                    withAlpha(stroke, Color.alpha(stroke) / 2),
                    density,
                ),
            )
            addState(
                intArrayOf(android.R.attr.state_focused),
                roundedBackground(color, 11 * density, OfflinePalette.focus, density),
            )
            addState(intArrayOf(), roundedBackground(color, 11 * density, stroke, density))
        }
        return ripple(
            content,
            roundedBackground(Color.WHITE, 11 * density, Color.TRANSPARENT, density),
        )
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

    private fun setMinimumTarget(button: Button, density: Float) {
        val target = (48 * density).toInt()
        button.minWidth = target
        button.minHeight = target
        button.minimumWidth = target
        button.minimumHeight = target
    }

    private fun interactiveBackground(
        context: Context,
        radiusDp: Int,
        primary: Boolean,
        danger: Boolean = false,
        selected: Boolean,
        ghost: Boolean = false,
    ): Drawable {
        val density = context.resources.displayMetrics.density
        fun shape(color: Int, stroke: Int = OfflinePalette.border) =
            roundedBackground(color, radiusDp * density, stroke, density)
        val normal = when {
            primary -> shape(OfflinePalette.accent, OfflinePalette.accent)
            danger -> shape(withAlpha(OfflinePalette.error, 0x0D), withAlpha(OfflinePalette.error, 0x66))
            else -> shape(
                if (ghost) Color.TRANSPARENT else withAlpha(OfflinePalette.foreground, 0x0A),
                if (ghost) Color.TRANSPARENT else OfflinePalette.border,
            )
        }
        val selectedShape = when {
            primary -> shape(OfflinePalette.accent, OfflinePalette.accent)
            danger -> shape(withAlpha(OfflinePalette.error, 0x1F), withAlpha(OfflinePalette.error, 0x88))
            else -> shape(withAlpha(OfflinePalette.accent, 0x1F), withAlpha(OfflinePalette.accent, 0x47))
        }
        val focused = when {
            primary -> shape(Color.rgb(218, 246, 202), OfflinePalette.focus)
            danger -> shape(withAlpha(OfflinePalette.error, 0x18), OfflinePalette.focus)
            else -> shape(withAlpha(OfflinePalette.foreground, 0x10), OfflinePalette.focus)
        }
        val content = StateListDrawable().apply {
            addState(
                intArrayOf(-android.R.attr.state_enabled),
                shape(
                    when {
                        primary -> withAlpha(OfflinePalette.accent, 0x55)
                        ghost -> Color.TRANSPARENT
                        else -> withAlpha(OfflinePalette.panelRaised, 0x66)
                    },
                    if (ghost) Color.TRANSPARENT else withAlpha(OfflinePalette.border, 0x66),
                ),
            )
            if (selected) {
                addState(
                    intArrayOf(android.R.attr.state_focused, android.R.attr.state_selected),
                    if (primary) focused else shape(withAlpha(OfflinePalette.accent, 0x1F), OfflinePalette.focus),
                )
            }
            addState(intArrayOf(android.R.attr.state_focused), focused)
            addState(
                intArrayOf(android.R.attr.state_pressed),
                when {
                    primary -> shape(Color.rgb(218, 246, 202), OfflinePalette.accent)
                    danger -> shape(withAlpha(OfflinePalette.error, 0x24), withAlpha(OfflinePalette.error, 0x88))
                    else -> shape(withAlpha(OfflinePalette.foreground, 0x1F), withAlpha(OfflinePalette.foreground, 0x24))
                },
            )
            if (selected) addState(intArrayOf(android.R.attr.state_selected), selectedShape)
            addState(intArrayOf(), normal)
        }
        return ripple(
            content,
            roundedBackground(Color.WHITE, radiusDp * density, Color.TRANSPARENT, density),
        )
    }

    private fun inputBackground(context: Context): Drawable {
        val density = context.resources.displayMetrics.density
        fun shape(color: Int, stroke: Int) = roundedBackground(color, 10 * density, stroke, density)
        val content = StateListDrawable().apply {
            addState(
                intArrayOf(-android.R.attr.state_enabled),
                shape(withAlpha(OfflinePalette.input, 0x88), withAlpha(OfflinePalette.border, 0x66)),
            )
            addState(intArrayOf(android.R.attr.state_focused), shape(OfflinePalette.input, OfflinePalette.focus))
            addState(intArrayOf(), shape(OfflinePalette.input, OfflinePalette.border))
        }
        return ripple(
            content,
            roundedBackground(Color.WHITE, 10 * density, Color.TRANSPARENT, density),
        )
    }

    private fun controlBackground(context: Context): Drawable {
        val density = context.resources.displayMetrics.density
        fun shape(color: Int, stroke: Int) = roundedBackground(color, 8 * density, stroke, density)
        val content = StateListDrawable().apply {
            addState(intArrayOf(android.R.attr.state_focused), shape(Color.TRANSPARENT, OfflinePalette.focus))
            addState(intArrayOf(), shape(Color.TRANSPARENT, Color.TRANSPARENT))
        }
        return ripple(
            content,
            roundedBackground(Color.WHITE, 8 * density, Color.TRANSPARENT, density),
        )
    }

    private fun iconColors(primary: Boolean) = ColorStateList(
        arrayOf(
            intArrayOf(-android.R.attr.state_enabled),
            intArrayOf(android.R.attr.state_selected),
            intArrayOf(),
        ),
        intArrayOf(
            withAlpha(if (primary) OfflinePalette.accentInk else OfflinePalette.muted, 0x66),
            if (primary) OfflinePalette.accentInk else OfflinePalette.accent,
            if (primary) OfflinePalette.accentInk else OfflinePalette.foreground,
        ),
    )

    private fun buttonColors(primary: Boolean, danger: Boolean) = ColorStateList(
        arrayOf(
            intArrayOf(-android.R.attr.state_enabled),
            intArrayOf(),
        ),
        intArrayOf(
            withAlpha(
                when {
                    primary -> OfflinePalette.accentInk
                    danger -> OfflinePalette.error
                    else -> OfflinePalette.foreground
                },
                0x66,
            ),
            when {
                primary -> OfflinePalette.accentInk
                danger -> OfflinePalette.error
                else -> OfflinePalette.foreground
            },
        ),
    )

    private fun selectionColors() = ColorStateList(
        arrayOf(
            intArrayOf(-android.R.attr.state_enabled),
            intArrayOf(android.R.attr.state_selected),
            intArrayOf(android.R.attr.state_focused),
            intArrayOf(),
        ),
        intArrayOf(
            withAlpha(OfflinePalette.muted, 0x66),
            OfflinePalette.accent,
            OfflinePalette.foreground,
            OfflinePalette.muted,
        ),
    )

    private fun textColors(color: Int) = ColorStateList(
        arrayOf(
            intArrayOf(-android.R.attr.state_enabled),
            intArrayOf(),
        ),
        intArrayOf(withAlpha(color, 0x66), color),
    )

    private fun checkColors() = ColorStateList(
        arrayOf(
            intArrayOf(-android.R.attr.state_enabled, android.R.attr.state_checked),
            intArrayOf(-android.R.attr.state_enabled),
            intArrayOf(android.R.attr.state_checked),
            intArrayOf(android.R.attr.state_focused),
            intArrayOf(),
        ),
        intArrayOf(
            withAlpha(OfflinePalette.accent, 0x66),
            withAlpha(OfflinePalette.muted, 0x66),
            OfflinePalette.accent,
            OfflinePalette.focus,
            OfflinePalette.muted,
        ),
    )

    private fun ripple(content: Drawable, mask: Drawable) = RippleDrawable(
        ColorStateList.valueOf(withAlpha(OfflinePalette.foreground, 0x24)),
        content,
        mask,
    )

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
        showPlaceholder(view, targetPixels)
        view.tag = null
        if (path.isNullOrBlank()) return
        val cacheKey = "$path:$targetPixels"
        view.tag = cacheKey
        cache.get(cacheKey)?.let {
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
            if (bitmap != null) cache.put(cacheKey, bitmap)
            if (view.tag == cacheKey && bitmap != null) showArtwork(view, bitmap)
        }
    }

    private fun showPlaceholder(view: ImageView, targetPixels: Int) {
        val iconPixels = minOf(targetPixels / 2, (view.resources.displayMetrics.density * 64).toInt())
        val padding = ((targetPixels - iconPixels) / 2).coerceAtLeast(0)
        view.setPadding(padding, padding, padding, padding)
        view.scaleType = ImageView.ScaleType.FIT_CENTER
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
