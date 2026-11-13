plugins {
    id("com.android.application")
}

val appVersion = "0.1.0"

android {
    namespace = "io.github.ahbanavi.smscleaner"
    compileSdk = 36

    defaultConfig {
        applicationId = "io.github.ahbanavi.smscleaner"
        minSdk = 26
        targetSdk = 36
        versionCode = 1
        versionName = appVersion
        ndk { abiFilters += "arm64-v8a" }
    }

    buildTypes {
        release {
            isMinifyEnabled = false
        }
    }

    // The Go server ships as lib*.so so Android extracts it to nativeLibraryDir,
    // the one app directory it may be executed from.
    packaging {
        jniLibs { useLegacyPackaging = true }
    }
}

dependencies {
    implementation("androidx.work:work-runtime:2.12.0")
}

// Builds the Go server (the repository root) for Android arm64 into jniLibs.
val repoRoot = rootProject.projectDir.parentFile
val goBinary = layout.projectDirectory.file("src/main/jniLibs/arm64-v8a/libsmscleaner.so")
val buildGo = tasks.register<Exec>("buildGo") {
    description = "Builds the sms-cleaner Go server for android/arm64"
    workingDir = repoRoot
    environment("GOOS", "android")
    environment("GOARCH", "arm64")
    environment("CGO_ENABLED", "0")
    commandLine(
        "go", "build", "-trimpath",
        "-ldflags=-s -w -X main.version=$appVersion",
        "-o", goBinary.asFile.absolutePath, ".",
    )
    inputs.files(
        fileTree(repoRoot) {
            include("*.go", "go.mod", "go.sum", "internal/**")
        },
    )
    outputs.file(goBinary)
}
tasks.named("preBuild") { dependsOn(buildGo) }
