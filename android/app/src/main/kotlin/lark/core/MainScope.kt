package lark.core

import android.util.Log
import kotlinx.coroutines.CoroutineExceptionHandler
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob

/**
 * A process-long scope on the main thread (immediate dispatcher): a task that fails is logged under [tag] by its
 * exception's class only (a message could hold page or server data), and the process goes on.
 */
fun loggingMainScope(tag: String): CoroutineScope =
    CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate + CoroutineExceptionHandler { _, e ->
        Log.e(tag, "an app task failed: ${e.javaClass.name}")
    })
