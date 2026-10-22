package org.eu.eslzzyl.krkrgo.bridge

import androidx.lifecycle.ViewModel
import org.eu.eslzzyl.krkrgo.mobile.krkrgo.Krkrgo
import org.eu.eslzzyl.krkrgo.catalog.ProjectEntry

class GameHost : ViewModel() {
    fun open(project: ProjectEntry): Result<Unit> = runCatching {
        Krkrgo.openGame(project.path)
    }

    fun pause() {
        Krkrgo.pauseGame()
    }

    fun resume() {
        Krkrgo.resumeGame()
    }

    fun close() {
        Krkrgo.closeGame()
    }

    override fun onCleared() {
        close()
        super.onCleared()
    }
}
