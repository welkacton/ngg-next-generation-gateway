// 最小 Android app 模块骨架（不是完整可编译工程）。
//
// 一个标准 Android Studio 工程还需要根目录的 settings.gradle.kts 与 build.gradle.kts
// 来声明 com.android.application / org.jetbrains.kotlin.android 插件版本，以及
// gradle wrapper。本文件只负责 app 模块本身，详见 android/README.md。
//
// 共享 Go 核心（仓库 internal/mobile）由 platforms/build-mobile.sh 经 gomobile bind
// 生成 bin/platforms/mobile/ngg.aar，需手动拷贝到本模块的 libs/ 目录后再构建。

plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

android {
    namespace = "com.ngg.gateway"
    compileSdk = 35

    defaultConfig {
        applicationId = "com.ngg.gateway"
        minSdk = 26
        targetSdk = 35
        versionCode = 1
        versionName = "0.1.0-scaffold"
    }

    // 本骨架不含任何 Activity / 布局 / 资源，仅提供 Kotlin 薄封装。
    // TODO: 实现原生 UI、VpnService 与权限声明（AndroidManifest.xml）后补充。
}

dependencies {
    // gomobile bind 生成的共享 Go 核心（真实 REST 客户端，不是模拟器）。
    implementation(files("libs/ngg.aar"))

    // TODO: 视后续原生 UI 需要添加 AndroidX / Compose 等依赖。
}
