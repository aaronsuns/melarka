# Only for the instrumented tests run against the R8 build (r8test, build.gradle.kts): classes the test libraries
# (androidx.test, Guava, Error Prone annotations) mention but that are not on the device. Not part of release.
-dontwarn androidx.concurrent.futures.SuspendToFutureAdapter
-dontwarn javax.lang.model.element.Modifier

# The test APK (androidx.test) calls into the Kotlin standard library and coroutines of the app APK, and R8 only
# knows what the app itself uses (kotlin.LazyKt was removed: the runner crashed in onCreate). Keep them whole in
# this build type; the app's own code and the other libraries are still shrunk and obfuscated.
-keep class kotlin.** { *; }
-keep class kotlinx.coroutines.** { *; }

# The tests also call OkHttp's RequestBody.create and the app's test hooks (getServicesBuilt, getAdoptedPreloads, ...)
# by name. Keep the members (not the classes: those are still shrunk and renamed unless something refers to them).
-keep class okhttp3.** { *; }
-keepclassmembers class lark.** { *; }
# ... and Media3's SessionToken constructor, which only the test's MediaController uses.
-keep class androidx.media3.session.** { *; }
