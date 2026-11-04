// HarmonyOS / ArkTS 接入骨架（EntryAbility）。
//
// 能力边界：这是 **骨架 / scaffold**，不是成品 App。
// - HarmonyOS **不在 gomobile 的支持目标内**，不能直接复用 Android AAR 或 iOS XCFramework；
// - 共享 Go 核心（internal/mobile）在本平台有两种候选接入路径（均尚未实现，见下）；
// - VPN Kit（VpnExtensionAbility）、原生 UI 页面、Token 安全存储 **均未实现**；
// - 本文件仅给出标准 UIAbility 生命周期骨架，不含任何网络或 VPN 逻辑。
//
// 候选接入路径（二选一，均待开发）：
//   路径 A：把共享 Go 核心以 -buildmode=c-shared 编出 C ABI，再用 NAPI（C/C++）
//           写一层桥接，供 ArkTS 调用。需要为 mobile.Gateway 增加 C 导出函数，
//           当前仓库没有这层导出，属于待实现项。
//   路径 B：不经绑定库，直接由 ArkTS 用 @ohos.net.http 访问控制面
//           http://127.0.0.1:9090/api/v1（Bearer Token，默认仅本机回环）。
//           这是较简单、且与控制面真实 REST 语义一致的路径，但同样尚未实现。

import UIAbility from '@ohos.app.ability.UIAbility';
import hilog from '@ohos.hilog';
import window from '@ohos.window';
import type Want from '@ohos.app.ability.Want';
import type AbilityConstant from '@ohos.app.ability.AbilityConstant';
import type { BusinessError } from '@ohos.base';

const TAG: string = 'NGGEntryAbility';
const DOMAIN: number = 0xFF00;

export default class EntryAbility extends UIAbility {
  onCreate(want: Want, launchParam: AbilityConstant.LaunchParam): void {
    hilog.info(DOMAIN, TAG, 'NGG scaffold onCreate, ability: %{public}s', this.context.abilityInfo.name);

    // TODO: 初始化控制面客户端（base_url / token 来自安全录入，禁止硬编码）。
    // TODO: 路径 A 时在此加载 NAPI 模块并创建共享核心 Gateway 实例；
    //       路径 B 时在此构造 http 请求封装（Authorization: Bearer <token>）。
    // TODO: 调用前用 Connectable()/GET /api/v1/health 真实探测可达性。
  }

  onWindowStageCreate(windowStage: window.WindowStage): void {
    hilog.info(DOMAIN, TAG, 'onWindowStageCreate');

    // pages/Index 仅为占位：遥控器友好 / 普通 ArkUI 页面尚未实现。
    windowStage.loadContent('pages/Index', (err: BusinessError) => {
      if (err.code) {
        hilog.error(DOMAIN, TAG, 'loadContent failed: %{public}d', err.code);
        return;
      }
      hilog.info(DOMAIN, TAG, 'loadContent succeeded');
    });

    // TODO: VPN Kit 接入：
    //  1. 实现 VpnExtensionAbility 并在 module.json5 中声明；
    //  2. 建立 TUN 后把流量导向 NGG ingress（默认 127.0.0.1:18080）
    //     或 SOCKS5/HTTP CONNECT 入口，并处理 DNS（默认 127.0.0.1:18053）；
    //  3. 处理 fail_closed、断线重连、状态回传 UI。
    // 以上全部未实现。
  }

  onWindowStageDestroy(): void {
    hilog.info(DOMAIN, TAG, 'onWindowStageDestroy');
  }

  onForeground(): void {
    hilog.info(DOMAIN, TAG, 'onForeground');
  }

  onBackground(): void {
    hilog.info(DOMAIN, TAG, 'onBackground');
  }

  onDestroy(): void {
    hilog.info(DOMAIN, TAG, 'onDestroy');
    // TODO: 释放控制面客户端 / NAPI 句柄 / VPN 会话。
  }
}
