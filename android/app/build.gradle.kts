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

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
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
    testImplementation(libs.junit)
}
