package org.eu.eslzzyl.krkrgo.ui

import android.view.ViewGroup
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.viewinterop.AndroidView
import org.eu.eslzzyl.krkrgo.mobile.krkrgo.EbitenView

@Composable
fun GameScreen() {
    AndroidView(
        modifier = Modifier.fillMaxSize(),
        factory = { context ->
            EbitenView(context).apply {
                isFocusable = true
                isFocusableInTouchMode = true
                layoutParams = ViewGroup.LayoutParams(
                    ViewGroup.LayoutParams.MATCH_PARENT,
                    ViewGroup.LayoutParams.MATCH_PARENT,
                )
                requestFocus()
            }
        },
    )
}
