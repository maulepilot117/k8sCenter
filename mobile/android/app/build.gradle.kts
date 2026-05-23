import java.io.FileInputStream
import java.util.Properties

plugins {
    id("com.android.application")
    id("kotlin-android")
    // The Flutter Gradle Plugin must be applied after the Android and Kotlin Gradle plugins.
    id("dev.flutter.flutter-gradle-plugin")
}

// Load upload signing properties from mobile/android/key.properties when
// present. The file is operator-provided (not in the repo) and lists:
//   storeFile=upload-keystore.jks   (path relative to mobile/android/app/)
//   storePassword=...
//   keyAlias=upload
//   keyPassword=...
// CI writes this file from the ANDROID_UPLOAD_* secrets before invoking
// `flutter build appbundle --release`. Finding P1-5 of the 2026-05-22
// security audit: release builds must NOT fall back to debug signing.
val keyPropertiesFile = rootProject.file("key.properties")
val keyProperties = Properties().apply {
    if (keyPropertiesFile.exists()) {
        load(FileInputStream(keyPropertiesFile))
    }
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

    signingConfigs {
        create("upload") {
            val storeFileProp = keyProperties.getProperty("storeFile")
            val storePasswordProp = keyProperties.getProperty("storePassword")
            val keyAliasProp = keyProperties.getProperty("keyAlias")
            val keyPasswordProp = keyProperties.getProperty("keyPassword")
            if (storeFileProp != null && storePasswordProp != null &&
                keyAliasProp != null && keyPasswordProp != null) {
                storeFile = file(storeFileProp)
                storePassword = storePasswordProp
                keyAlias = keyAliasProp
                keyPassword = keyPasswordProp
            }
        }
    }

    buildTypes {
        release {
            val uploadConfigured = keyProperties.getProperty("storeFile") != null &&
                keyProperties.getProperty("storePassword") != null &&
                keyProperties.getProperty("keyAlias") != null &&
                keyProperties.getProperty("keyPassword") != null
            if (uploadConfigured) {
                signingConfig = signingConfigs.getByName("upload")
            } else {
                // Fail any actual release build, but allow Gradle's
                // configuration phase to proceed (so `flutter analyze`,
                // `flutter test`, IDE imports, etc. don't break). The
                // check fires only when a release variant is being
                // assembled.
                gradle.taskGraph.whenReady {
                    if (allTasks.any { task ->
                        task.name.contains("Release", ignoreCase = true) &&
                            (task.name.startsWith("assemble") || task.name.startsWith("bundle") ||
                             task.name.startsWith("package"))
                    }) {
                        throw GradleException(
                            "Cannot build release variant: mobile/android/key.properties " +
                                "is missing or incomplete. CI must write it from the " +
                                "ANDROID_UPLOAD_* secrets before `flutter build " +
                                "appbundle --release`. (Finding P1-5)"
                        )
                    }
                }
            }
        }
    }
}

flutter {
    source = "../.."
}
