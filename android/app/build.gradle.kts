plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

// CI 与本地通过环境变量提供正式签名；没配置时退回 debug 签名，方便本地直接跑 release。
val releaseKeystore = System.getenv("FOYER_KEYSTORE_FILE")

android {
    namespace = "cc.elysia.foyer"
    compileSdk = 35

    defaultConfig {
        applicationId = "cc.elysia.foyer"
        minSdk = 26
        targetSdk = 35
        versionCode = 1
        versionName = "1.0"
    }

    signingConfigs {
        if (!releaseKeystore.isNullOrBlank()) {
            create("release") {
                storeFile = file(releaseKeystore)
                storePassword = System.getenv("FOYER_KEYSTORE_PASSWORD")
                keyAlias = System.getenv("FOYER_KEY_ALIAS")
                keyPassword = System.getenv("FOYER_KEY_PASSWORD")
            }
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = false
            signingConfig = if (releaseKeystore.isNullOrBlank()) {
                signingConfigs.getByName("debug")
            } else {
                signingConfigs.getByName("release")
            }
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    kotlin {
        compilerOptions {
            jvmTarget.set(org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_17)
        }
    }
}

dependencies {
    implementation("org.jsoup:jsoup:1.18.1")
    implementation("androidx.swiperefreshlayout:swiperefreshlayout:1.1.0")
    implementation("com.google.android.material:material:1.12.0")
}
