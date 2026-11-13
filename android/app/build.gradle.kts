plugins {
    id("com.android.application")
}

// CI sets these from the run number; local builds are 1 / "dev".
val appVersionCode = System.getenv("SMSC_VERSION_CODE")?.toIntOrNull() ?: 1
val appVersionName = System.getenv("SMSC_VERSION_NAME") ?: "dev"

// The release key, from the environment (CI secrets, or ~/.android/sms-cleaner/signing.env
// locally). Without it, release builds fall back to the debug key and won't install over
// a release from GitHub.
val keystorePath: String? = System.getenv("SMSC_KEYSTORE")?.takeIf { it.isNotBlank() && file(it).exists() }

android {
    namespace = "io.github.ahbanavi.smscleaner"
    compileSdk = 36

    defaultConfig {
        applicationId = "io.github.ahbanavi.smscleaner"
        minSdk = 26
        targetSdk = 36
        versionCode = appVersionCode
        versionName = appVersionName
        ndk { abiFilters += "arm64-v8a" }
    }

    signingConfigs {
        if (keystorePath != null) {
            create("release") {
                storeFile = file(keystorePath)
                storePassword = System.getenv("SMSC_KEYSTORE_PASSWORD")
                keyAlias = System.getenv("SMSC_KEY_ALIAS")
                keyPassword = System.getenv("SMSC_KEY_PASSWORD")
            }
        }
    }

    buildTypes {
        val releaseKey = signingConfigs.findByName("release")
        release {
            isMinifyEnabled = true
            isShrinkResources = true
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"))
            signingConfig = releaseKey ?: signingConfigs.getByName("debug")
        }
        debug {
            // With the release key at hand, debug builds use it too, so a development
            // build installs over the GitHub release (and back) without losing data.
            if (releaseKey != null) signingConfig = releaseKey
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
        "-ldflags=-s -w -X main.version=$appVersionName",
        "-o", goBinary.asFile.absolutePath, ".",
    )
    inputs.files(
        fileTree(repoRoot) {
            include("*.go", "go.mod", "go.sum", "internal/**")
        },
    )
    inputs.property("version", appVersionName)
    outputs.file(goBinary)
}
tasks.named("preBuild") { dependsOn(buildGo) }
