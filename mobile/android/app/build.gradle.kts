plugins {
    id("com.android.application")
    id("kotlin-android")
    // The Flutter Gradle Plugin must be applied after the Android and Kotlin Gradle plugins.
    id("dev.flutter.flutter-gradle-plugin")
}

android {
    namespace = "io.kubecenter.kubecenter"
    compileSdk = flutter.compileSdkVersion
    ndkVersion = flutter.ndkVersion

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    kotlinOptions {
        jvmTarget = JavaVersion.VERSION_17.toString()
    }

    defaultConfig {
        // TODO: Specify your own unique Application ID (https://developer.android.com/studio/build/application-id.html).
        applicationId = "io.kubecenter.kubecenter"
        // You can update the following values to match your application needs.
        // For more information, see: https://flutter.dev/to/review-gradle-config.
        minSdk = flutter.minSdkVersion
        targetSdk = flutter.targetSdkVersion
        // versionCode comes from the `-PversionCode` Gradle property when
        // set (Fastlane forwards GITHUB_RUN_NUMBER * 10 + RUN_ATTEMPT-1
        // for unique Play uploads on every push and re-run); otherwise
        // falls back to flutter.versionCode for local dev.
        versionCode = (project.findProperty("versionCode") as String?)?.toIntOrNull()
            ?: flutter.versionCode
        versionName = flutter.versionName

        // Universal Link host. Operators set this via gradle.properties
        // or `-PuniversalLinkHost=<domain>` so the AndroidManifest's HTTPS
        // intent-filter resolves at build time. Empty default disables
        // the HTTPS App Links filter entirely (keeps the manifest valid
        // for homelab builds that rely on the k8scenter:// custom scheme),
        // avoiding the Android-13+ verifier flooding logcat with
        // "host=''" verification failures.
        val universalLinkHost = (project.findProperty("universalLinkHost") as String?) ?: ""
        manifestPlaceholders["universalLinkHost"] = universalLinkHost
        manifestPlaceholders["universalLinkEnabled"] =
            if (universalLinkHost.isNotEmpty()) "true" else "false"
    }

    buildTypes {
        release {
            // TODO: Add your own signing config for the release build.
            // Signing with the debug keys for now, so `flutter run --release` works.
            signingConfig = signingConfigs.getByName("debug")
        }
    }
}

flutter {
    source = "../.."
}
