import java.io.File

plugins {
    id("com.android.application")
}

val versionFile = rootProject.file("../internal/version/VERSION")
val rawVersion = if (versionFile.isFile) versionFile.readText().trim() else "0.2.1"
val coreParts = rawVersion.substringBefore("-").split(".")
val computedVersionCode =
    (coreParts.getOrNull(0)?.toIntOrNull() ?: 0) * 10000 +
    (coreParts.getOrNull(1)?.toIntOrNull() ?: 2) * 100 +
    (coreParts.getOrNull(2)?.toIntOrNull() ?: 0)

val envKeystorePath = System.getenv("ANDROID_KEYSTORE_PATH")?.takeIf { it.isNotBlank() }
val fallbackKeystoreFile = layout.buildDirectory.file("generated-keystore/bgmcat-release.p12").get().asFile

val ensureFallbackKeystore by tasks.registering {
    onlyIf { envKeystorePath == null }
    outputs.file(fallbackKeystoreFile)
    doLast {
        if (!fallbackKeystoreFile.isFile) {
            fallbackKeystoreFile.parentFile.mkdirs()
            val javaHome = System.getProperty("java.home")
            val keytoolBin = File(javaHome, if (System.getProperty("os.name").lowercase().contains("win")) "bin/keytool.exe" else "bin/keytool")
            val pb = ProcessBuilder(
                keytoolBin.absolutePath,
                "-genkeypair",
                "-v",
                "-storetype", "PKCS12",
                "-keystore", fallbackKeystoreFile.absolutePath,
                "-alias", "bgmcat",
                "-keyalg", "RSA",
                "-keysize", "2048",
                "-validity", "10000",
                "-storepass", "bgmcat-release-key",
                "-keypass", "bgmcat-release-key",
                "-dname", "CN=Bangumi Cat, OU=OpenSource, O=SuInk, L=Tokyo, ST=Tokyo, C=JP"
            )
            pb.inheritIO()
            val code = pb.start().waitFor()
            check(code == 0) { "keytool failed with exit code $code" }
        }
    }
}

android {
    namespace = "io.github.suink.bgmcat"
    compileSdk = 35

    defaultConfig {
        applicationId = "io.github.suink.bgmcat"
        minSdk = 26
        targetSdk = 35
        versionCode = computedVersionCode.coerceAtLeast(1)
        versionName = rawVersion
    }

    signingConfigs {
        create("release") {
            if (envKeystorePath != null) {
                storeFile = File(envKeystorePath)
                storePassword = System.getenv("ANDROID_KEYSTORE_PASSWORD") ?: ""
                keyAlias = System.getenv("ANDROID_KEY_ALIAS") ?: "bgmcat"
                keyPassword = System.getenv("ANDROID_KEY_PASSWORD") ?: System.getenv("ANDROID_KEYSTORE_PASSWORD") ?: ""
            } else {
                storeFile = fallbackKeystoreFile
                storePassword = "bgmcat-release-key"
                keyAlias = "bgmcat"
                keyPassword = "bgmcat-release-key"
            }
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = false
            isShrinkResources = false
            signingConfig = signingConfigs.getByName("release")
        }
        debug {
            signingConfig = signingConfigs.getByName("release")
        }
    }

    packaging {
        jniLibs {
            // The backend is an executable shipped as lib*.so; it must be extracted to
            // nativeLibraryDir, the only app directory Android lets us exec() from.
            useLegacyPackaging = true
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
}

dependencies {
    // WebViewCompat.addDocumentStartJavaScript: injects the local API token per origin.
    implementation("androidx.webkit:webkit:1.12.1")
}

// The Go backend is cross-compiled outside Gradle (Go and the NDK are not Gradle
// plugins); fail early with the fix instead of shipping an APK that cannot start.
val backendAbis = listOf("arm64-v8a", "x86_64")
val verifyBundledBackend by tasks.registering {
    val libs = backendAbis.map { file("src/main/jniLibs/$it/libbgmcat.so") }
    inputs.files(libs).optional()
    doLast {
        val missing = libs.filter { !it.isFile }
        check(missing.isEmpty()) {
            "缺少内置后端：${missing.joinToString { it.parentFile.name }}。请先在仓库根目录运行 npm run build 和 scripts/build-android-backend.sh"
        }
    }
}
tasks.named("preBuild") { dependsOn(verifyBundledBackend) }

tasks.matching { it.name.contains("Release") && it.name.contains("Sign", ignoreCase = true) || it.name == "validateSigningRelease" }.configureEach {
    dependsOn(ensureFallbackKeystore)
}
