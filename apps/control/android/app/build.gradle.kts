plugins {
    id("com.android.application")
    id("kotlin-android")
    // The Flutter Gradle Plugin must be applied after the Android and Kotlin Gradle plugins.
    id("dev.flutter.flutter-gradle-plugin")
}

val controlKeystore = providers.environmentVariable("CONTROL_ANDROID_KEYSTORE").orNull
val controlStorePassword = providers.environmentVariable("CONTROL_ANDROID_STORE_PASSWORD").orNull
val controlKeyAlias = providers.environmentVariable("CONTROL_ANDROID_KEY_ALIAS").orNull
val controlKeyPassword = providers.environmentVariable("CONTROL_ANDROID_KEY_PASSWORD").orNull
val releaseRequested = gradle.startParameter.taskNames.any { it.contains("Release", ignoreCase = true) }
if (releaseRequested && listOf(controlKeystore, controlStorePassword, controlKeyAlias, controlKeyPassword).any { it.isNullOrBlank() }) {
    throw GradleException("protected Control Android signing inputs are required")
}

android {
    namespace = "io.jastreamer.control"
    compileSdk = flutter.compileSdkVersion
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_11
        targetCompatibility = JavaVersion.VERSION_11
    }

    kotlinOptions {
        jvmTarget = JavaVersion.VERSION_11.toString()
    }

    defaultConfig {
        applicationId = "io.jastreamer.control"
        minSdk = maxOf(flutter.minSdkVersion, 23)
        targetSdk = flutter.targetSdkVersion
        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
        versionCode = flutter.versionCode
        versionName = flutter.versionName
    }

    signingConfigs {
        create("controlRelease") {
            storeFile = controlKeystore?.let(::file)
            storePassword = controlStorePassword
            keyAlias = controlKeyAlias
            keyPassword = controlKeyPassword
        }
    }

    buildTypes {
        release {
            signingConfig = signingConfigs.getByName("controlRelease")
        }
    }
}

dependencies {
    androidTestImplementation("androidx.test:core:1.3.0")
    androidTestImplementation("androidx.test.ext:junit:1.1.2")
    androidTestImplementation("androidx.test:runner:1.3.0")
}

flutter {
    source = "../.."
}
