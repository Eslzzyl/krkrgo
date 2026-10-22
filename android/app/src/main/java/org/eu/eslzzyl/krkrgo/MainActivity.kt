package org.eu.eslzzyl.krkrgo

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.viewModels
import androidx.compose.runtime.remember
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import org.eu.eslzzyl.krkrgo.bridge.GameHost
import org.eu.eslzzyl.krkrgo.catalog.InMemoryProjectCatalog
import org.eu.eslzzyl.krkrgo.ui.GameScreen
import org.eu.eslzzyl.krkrgo.ui.KrkrgoTheme
import org.eu.eslzzyl.krkrgo.ui.LauncherScreen

class MainActivity : ComponentActivity() {
    private val gameHost by viewModels<GameHost>()

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        setContent {
            KrkrgoTheme {
                val catalog = remember { InMemoryProjectCatalog() }
                var gameVisible by remember { mutableStateOf(false) }
                var selectedProjectId by remember { mutableStateOf<String?>(null) }

                if (gameVisible) {
                    GameScreen(
                        onBack = {
                            gameHost.close()
                            selectedProjectId = null
                            gameVisible = false
                        },
                    )
                } else {
                    LauncherScreen(
                        catalog = catalog,
                        selectedProjectId = selectedProjectId,
                        onLaunch = { project ->
                            gameHost.open(project).onSuccess {
                                selectedProjectId = project.id
                                gameVisible = true
                            }
                        },
                    )
                }
            }
        }
    }

    override fun onStart() {
        super.onStart()
        gameHost.resume()
    }

    override fun onStop() {
        gameHost.pause()
        super.onStop()
    }

    override fun onDestroy() {
        if (isFinishing) {
            gameHost.close()
        }
        super.onDestroy()
    }
}
