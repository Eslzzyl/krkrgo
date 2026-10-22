package org.eu.eslzzyl.krkrgo

import android.content.Intent
import android.content.pm.ActivityInfo
import android.os.Bundle
import androidx.activity.result.contract.ActivityResultContracts
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.viewModels
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.remember
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.platform.LocalContext
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.eu.eslzzyl.krkrgo.R
import org.eu.eslzzyl.krkrgo.bridge.GameHost
import org.eu.eslzzyl.krkrgo.catalog.PersistentProjectCatalog
import org.eu.eslzzyl.krkrgo.catalog.ProjectImporter
import org.eu.eslzzyl.krkrgo.ui.GameScreen
import org.eu.eslzzyl.krkrgo.ui.KrkrgoTheme
import org.eu.eslzzyl.krkrgo.ui.LauncherScreen

class MainActivity : ComponentActivity() {
    private val gameHost by viewModels<GameHost>()

    private fun setLauncherOrientation() {
        requestedOrientation = ActivityInfo.SCREEN_ORIENTATION_UNSPECIFIED
    }

    private fun setGameOrientation() {
        requestedOrientation = ActivityInfo.SCREEN_ORIENTATION_LANDSCAPE
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        setContent {
            KrkrgoTheme {
                val context = LocalContext.current
                val catalog = remember { PersistentProjectCatalog(context.applicationContext) }
                val coroutineScope = rememberCoroutineScope()
                var gameVisible by remember { mutableStateOf(false) }
                var selectedProjectId by remember { mutableStateOf<String?>(null) }
                var operationError by remember { mutableStateOf<String?>(null) }
                var isImporting by remember { mutableStateOf(false) }
                var isLaunching by remember { mutableStateOf(false) }

                val projectPicker = rememberLauncherForActivityResult(
                    contract = ActivityResultContracts.OpenDocumentTree(),
                ) { uri ->
                    if (uri != null) {
                        runCatching {
                            contentResolver.takePersistableUriPermission(
                                uri,
                                Intent.FLAG_GRANT_READ_URI_PERMISSION,
                            )
                        }
                        isImporting = true
                        coroutineScope.launch {
                            val result = withContext(Dispatchers.IO) {
                                ProjectImporter(context.applicationContext).import(uri)
                            }
                            result
                                .onSuccess { imported ->
                                    catalog.add(imported.name, imported.uri.toString())
                                }
                                .onFailure { error ->
                                    operationError = error.message
                                        ?: context.getString(R.string.error_unable_import_project)
                                }
                            isImporting = false
                        }
                    }
                }

                if (gameVisible) {
                    GameScreen()
                } else {
                    LauncherScreen(
                        catalog = catalog,
                        selectedProjectId = selectedProjectId,
                        operationError = operationError,
                        onDismissOperationError = { operationError = null },
                        isImporting = isImporting,
                        isLaunching = isLaunching,
                        onAddProject = { projectPicker.launch(null) },
                        onLaunch = { project ->
                            if (!isLaunching) {
                                isLaunching = true
                                coroutineScope.launch {
                                    val result = withContext(Dispatchers.IO) {
                                        gameHost.open(context, project)
                                    }
                                    result
                                        .onSuccess {
                                            selectedProjectId = project.id
                                            setGameOrientation()
                                            gameVisible = true
                                        }
                                        .onFailure { error ->
                                            operationError = error.message
                                                ?: context.getString(R.string.error_unable_launch_project)
                                        }
                                    isLaunching = false
                                }
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
