allprojects {
    repositories {
        google()
        mavenCentral()
    }
}

val newBuildDir: Directory =
    rootProject.layout.buildDirectory
        .dir("../../build")
        .get()
rootProject.layout.buildDirectory.value(newBuildDir)

subprojects {
    val newSubprojectBuildDir: Directory = newBuildDir.dir(project.name)
    project.layout.buildDirectory.value(newSubprojectBuildDir)
}
subprojects {
    project.evaluationDependsOn(":app")
}

// Several Flutter plugins (e.g. sentry_flutter 8.x) pin their Kotlin language
// and API version to 1.6, which the project's Kotlin 2.2.20 toolchain no longer
// supports ("Language version 1.6 is no longer supported; please, use version
// 1.8 or greater"). Raise any sub-project Kotlin compile task that requests a
// version below 1.8 up to 1.8. Raising a language/api version is backward
// compatible, and gating on the current value avoids forcing a version onto
// tasks that never set one. This fixes the whole class of plugins at once
// rather than patching each plugin's build.gradle individually.
subprojects {
    tasks.withType<org.jetbrains.kotlin.gradle.tasks.KotlinCompile>().configureEach {
        val floor = org.jetbrains.kotlin.gradle.dsl.KotlinVersion.KOTLIN_1_8
        compilerOptions {
            languageVersion.set(
                languageVersion.orNull?.let { if (it < floor) floor else it },
            )
            apiVersion.set(
                apiVersion.orNull?.let { if (it < floor) floor else it },
            )
        }
    }
}

tasks.register<Delete>("clean") {
    delete(rootProject.layout.buildDirectory)
}
