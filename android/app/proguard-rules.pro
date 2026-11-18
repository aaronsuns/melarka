# R8 rules for the release build (isMinifyEnabled in build.gradle.kts). Most libraries ship consumer rules inside
# their AARs (OkHttp, Media3, WorkManager, Compose, kotlinx.coroutines); these are the ones we add on top.
# Not built or run on a device yet when written: check the release APK on a phone or the emulator after changing
# a dependency (android/README.md).

# Readable crash stack traces.
-keepattributes SourceFile,LineNumberTable
-renamesourcefileattribute SourceFile

# kotlinx.serialization: serializers are found by name at run time (Companion.serializer(), $$serializer).
-keepattributes *Annotation*, InnerClasses
-dontnote kotlinx.serialization.AnnotationsKt
-keepclassmembers @kotlinx.serialization.Serializable class ** {
    *** Companion;
    *** INSTANCE;
    kotlinx.serialization.KSerializer serializer(...);
}
-keepclasseswithmembers class ** {
    kotlinx.serialization.KSerializer serializer(...);
}
-if @kotlinx.serialization.Serializable class **
-keepclassmembers class <1> {
    static <1>$Companion Companion;
}
-if @kotlinx.serialization.Serializable class ** {
    static **$* *;
}
-keepclassmembers class <2>$<3> {
    kotlinx.serialization.KSerializer serializer(...);
}

# OkHttp 5 optional TLS providers and annotations it only references when present.
-dontwarn org.bouncycastle.**
-dontwarn org.conscrypt.**
-dontwarn org.openjsse.**
-dontwarn javax.annotation.**

# Media3: the service the manifest names, and the session callbacks it reflects on.
-keep class lark.service.PlaybackService { *; }

# WorkManager: workers are created by class name from the persisted request.
-keep class * extends androidx.work.ListenableWorker {
    public <init>(android.content.Context, androidx.work.WorkerParameters);
}

# Manifest components (the car receiver, the playback receiver) are kept by AGP's generated rules; the
# WebView bridge uses addWebMessageListener (no @JavascriptInterface methods), so nothing to keep for it.
