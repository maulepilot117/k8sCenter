import java.io.FileInputStream
import java.util.Properties

plugins {
    id("com.android.application")
    id("kotlin-android")
    // The Flutter Gradle Plugin must be applied after the Android and Kotlin Gradle plugins.
    id("dev.flutter.flutter-gradle-plugin")
}

// CI writes key.properties to $RUNNER_TEMP/android-signing/ and exports the
// path via ANDROID_KEY_PROPERTIES_PATH so the secret never lives in the
// workspace (defense against accidental artifact-upload leaks per Finding P2-#15
// of the 2026-05-22 security audit). Local developers continue to use
// mobile/android/key.properties relative to rootProject. CI provisions the
// file from the ANDROID_UPLOAD_* secrets at run time; finding P1-5 of the
// audit: release builds must NOT fall back to debug signing.
val keyPropertiesFile = System.getenv("ANDROID_KEY_PROPERTIES_PATH")
    ?.let { java.io.File(it) }
    ?: rootProject.file("key.properties")

val keyProperties = Properties().apply {
    if (keyPropertiesFile.exists()) {
        load(FileInputStream(keyPropertiesFile))
    }
}

// Single source of truth for upload-key presence check (Finding P3-#21).
// Used by both signingConfigs.create("upload") and the tasks.matching guard.
val uploadConfigured: Boolean = listOf("storeFile", "storePassword", "keyAlias", "keyPassword")
    .all { keyProperties.getProperty(it) != null }

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
            if (uploadConfigured) {
                storeFile = file(keyProperties.getProperty("storeFile")!!)
                storePassword = keyProperties.getProperty("storePassword")!!
                keyAlias = keyProperties.getProperty("keyAlias")!!
                keyPassword = keyProperties.getProperty("keyPassword")!!
            }
        }
    }

    buildTypes {
        release {
            // Use upload signing when key.properties is fully configured (CI
            // sets ANDROID_KEY_PROPERTIES_PATH; local devs place key.properties
            // in mobile/android/). Falls back to debug signing for IDE imports,
            // `flutter analyze`, and `flutter test`. The tasks.matching block
            // below throws at execution time if a release-build/sign task runs
            // without upload signing configured — finding P1-5.
            signingConfig = if (uploadConfigured) {
                signingConfigs.getByName("upload")
            } else {
                signingConfigs.getByName("debug")
            }
        }
    }
}

// Defensive: if any release-variant build/sign task is in the graph and upload
// signing isn't configured, fail with a clear error attributed to the task
// itself rather than to a global listener. Targets the Android Gradle Plugin's
// actual signing task names; flavors like `releaseDebug` produce
// `signReleaseDebugBundle` not bare `signRelease...`, so the exact-match
// avoids the substring-matching false-positives flagged by Finding P2-#14.
tasks.matching { task ->
    val n = task.name
    n == "signReleaseBundle" || n == "signReleaseApk" ||
    n == "validateSigningRelease" || n == "packageRelease" ||
    n == "bundleRelease"
}.configureEach {
    doFirst {
        if (!uploadConfigured) {
            throw GradleException(
                "Cannot run ${this.name}: mobile/android/key.properties is " +
                "missing or incomplete. CI must write it from ANDROID_UPLOAD_* " +
                "secrets before `flutter build appbundle --release`. " +
                "(Finding P1-5)"
            )
        }
    }
}

flutter {
    source = "../.."
}
