package org.eu.eslzzyl.krkrgo.bridge

import android.content.Context
import androidx.lifecycle.ViewModel
import org.eu.eslzzyl.krkrgo.mobile.krkrgo.Krkrgo
import org.eu.eslzzyl.krkrgo.catalog.ProjectEntry
import org.eu.eslzzyl.krkrgo.storage.ContentUriStorage

class GameHost : ViewModel() {
    private var activeStorage: ContentUriStorage? = null

    fun open(context: Context, project: ProjectEntry): Result<Unit> = runCatching {
        val storage = ContentUriStorage(context.applicationContext, android.net.Uri.parse(project.uri))
        Krkrgo.openGameWithContentURI(project.uri, storage)
        activeStorage = storage
    }

    fun pause() {
        Krkrgo.pauseGame()
    }

    fun resume() {
        Krkrgo.resumeGame()
    }

    fun close() {
        Krkrgo.closeGame()
        activeStorage?.close()
        activeStorage = null
    }

    override fun onCleared() {
        close()
        super.onCleared()
    }
}
