import lark.build.larkVersion
import lark.build.versionCode as larkVersionCode

plugins {
    alias(libs.plugins.android.application)
    alias(libs.plugins.kotlin.android)
}

val appVersion = larkVersion(rootProject.projectDir.parentFile, providers.gradleProperty("larkVersion").orNull)

android {
    namespace = "lark.app"
    compileSdk = 35

    defaultConfig {
        applicationId = "io.github.aaronsuns.melarka"
        minSdk = 26
        targetSdk = 35
        versionName = appVersion
        versionCode = larkVersionCode(appVersion)
    }

    buildFeatures {
        buildConfig = true   // ServerUrl: the emulator host is allowed in debug builds only
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    testOptions {
        unitTests.isIncludeAndroidResources = true
    }

    sourceSets {
        // The version function is shared with the build: one implementation, tested here.
        getByName("test").java.srcDir("../buildSrc/src/main/kotlin")
    }
}

kotlin {
    jvmToolchain(17)
}

dependencies {
    implementation(libs.kotlinx.serialization.json)
    implementation(libs.kotlinx.coroutines.core)
    implementation(libs.kotlinx.coroutines.android)
    implementation(libs.okhttp)
    implementation(libs.media3.exoplayer)
    implementation(libs.media3.datasource.okhttp)
    implementation(libs.media3.session)
    implementation(libs.androidx.work.runtime)
    implementation(libs.androidx.webkit)
    implementation(libs.androidx.core)
    testImplementation(libs.junit)
    testImplementation(libs.kotlinx.coroutines.test)
    testImplementation(libs.okhttp.mockwebserver)
    testImplementation(libs.okhttp.tls)
    testImplementation(libs.robolectric)
    testImplementation(libs.androidx.test.core)
    testImplementation(libs.androidx.work.testing)
    testImplementation(libs.media3.test.utils)
    testImplementation(libs.media3.test.utils.robolectric)
}
