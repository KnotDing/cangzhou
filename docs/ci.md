# CI 与发布

GitHub 和 Gitea 分别运行各自工作流，默认分支为 `main`。向 `main` 推送或发起 Pull Request 时，两边的测试工作流会检查 Go 代码、执行竞态和实际 Kopia 集成测试，并构建、检查 x86-64 FPK。GitHub 同时保存短期测试工件；这些工件不是正式发布。

## 工具与构建

`scripts/fetch-ci-tools.sh` 下载飞牛 fnpack 1.2.3 Linux x86-64 和 Kopia 0.23.1 Linux x86-64，校验脚本内固定的 SHA-256 后放到 `tools/`。`scripts/build-fpk.sh` 使用 `packaging/fnos/` 和当前干净的 Git 提交构建，检查包内 manifest、UI 配置和 Linux x86-64 ELF 程序，输出：

- `artifacts/cangzhou-<版本>-x86.fpk`
- `artifacts/cangzhou-<版本>-source.tar.gz`
- `artifacts/SHA256SUMS`

源码包来自同一个 Git 提交，不含 Kopia 二进制。公开仓库不会跟踪 `tools/`、`artifacts/`、FPK、任务数据或凭据。`packaging/fnos/manifest` 是当前打包版本来源；运行时产生的数据不会纳入构建。

## 版本发布

1. 修改并提交 `packaging/fnos/manifest` 的版本，并编写 `releases/<版本>/README.md` 更新说明，完成验证。发布流程将该文件作为发布页正文。
2. 在 GitHub 和 Gitea 推送指向**同一提交**的 `v主版本.次版本.修订号` 标签，例如 `v0.4.6`。标签去掉 `v` 后必须与 manifest 完全一致；当前流程不接受带后缀的标签。
3. GitHub 标签工作流测试、构建并发布上述三个资产。也可手动运行该工作流并指定已有标签；它不会替你创建标签。
4. Gitea 优先等待 GitHub 的同标签发布，确认两端标签提交一致并核验 SHA-256 后镜像资产。若 GitHub 尚不可用，Gitea 会运行测试并独立构建；如果明确检测到两端标签指向不同提交，则拒绝发布。

Gitea 发布使用工作流内置的 `GITEA_TOKEN`；公开 GitHub 资产不需要额外令牌。Gitea runner 必须具备可用的 `ubuntu-latest` 标签，并能够访问 Go、GitHub 和飞牛工具下载地址。实际 token 权限和 runner 配置仍取决于服务端设置。Gitea 重试发布时，已有同名资产必须与本次文件校验一致；不一致时停止，不自动覆盖。

独立构建可能因构建环境和归档元数据产生不同字节；判断一致性应分别核验两端资产及其 `SHA256SUMS`，不能仅根据同名标签推断两端资产相同。推送标签后应检查两个平台的工作流、发布状态、资产和哈希；发布不等于设备已安装或验证。
