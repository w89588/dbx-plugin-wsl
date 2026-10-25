# 发布流程（官方商店）

本仓库是 WSL Manager 的插件源码仓库；官方商店目录在 **`t8y2/dbx-store`**（不是 `t8y2/dbx`）。本插件只支持 **windows-x64**，因此发布时只产出一个平台包。

Tag 注释、Release 标题与说明、PR 标题与正文默认使用中文；版本标签仍使用 `v<version>`。只有维护者明确要求时才创建或更新 PR，不因推送、打包或发布自动提交。

## 1. 版本与构建

1. 修改 `manifest.json` 的 `version`，并同步 `backend/main.go` 的 Sidecar 版本、`ui/index.html` 的 `APP_VERSION`、`docs/rpc-contract.md` 版本说明。
2. 运行测试：

   ```powershell
   node tools/manifest-check.mjs
   ```

3. 构建未签名候选包：

   ```powershell
   $env:GOTMPDIR='..\.gotmp'; $env:GOCACHE='..\.gocache'
   node ..\node_modules\@dbx-app\plugin-cli\bin\dbx-plugin.js package .
   ```

   产物：`dist/<plugin-id>-<version>-windows-x64.dbxp` 与同名 `.artifact.json`。

4. 生成商店与 Release 元数据：

   ```powershell
   node tools/release-meta.mjs --package dist/<plugin-id>-<version>-windows-x64.dbxp --repo w89588/dbx-plugin-wsl --tag v<version> --publisher w89588
   ```

   生成 `dist/release-candidates.json`、`dist/store/candidates/<id>.json`、`dist/store/publishers/<publisher>.json`。

## 2. 发布源码 Tag 与未签名候选 Release

```powershell
git tag -a v<version> -m "WSL 管理器 v<version>：<本次更新摘要>"
git push origin main v<version>
gh release create v<version> `
  dist/<plugin-id>-<version>-windows-x64.dbxp `
  dist/<plugin-id>-<version>-windows-x64.artifact.json `
  dist/release-candidates.json `
  --title "WSL 管理器 v<version>" --notes-file RELEASE_NOTES.md
```

Release 资产必须是**不可变、带版本号**的 HTTPS URL；`.dbxp` 不能提交进 Git。

> 本插件只构建 windows-x64，因此不使用模板里的多平台 GitHub Actions workflow（它会尝试为其他平台构建无效包）；发布由本地构建后手动上传完成。

## 3. 提交 `t8y2/dbx-store` PR

首次上架需要：`publishers/<publisher-id>.json` + `candidates/<plugin-id>.json`，一个 PR。

```powershell
gh repo fork t8y2/dbx-store --clone=false
git clone https://github.com/w89588/dbx-store.git
cd dbx-store
git checkout -b add-<plugin-id>-<version>
mkdir -p publishers candidates
# 把 dist/store 下生成的两个文件复制到对应目录
node scripts/validate.mjs   # 预期仅有 open candidate(s) awaiting DBX Store signing（等待维护者签名）
git add publishers/<publisher-id>.json candidates/<plugin-id>.json
git commit -m "提交 <name> <version> 候选包"
git push origin add-<plugin-id>-<version>
gh pr create --repo t8y2/dbx-store --base main --head w89588:add-<plugin-id>-<version> --title "更新 WSL 管理器至 <version>" --body-file <中文说明文件>
```

PR 描述需说明：插件能力、权限、数据访问、网络访问、原生 Sidecar 行为（见 `t8y2/dbx-store/CONTRIBUTING.md`）。

## 4. 审核、签名与合并（维护者执行，作者不能自助完成）

- CI 会保持失败并提示 `open candidate(s) awaiting DBX Store signing`，这是预期状态，阻止未签名内容被合并。
- 维护者审核后运行受保护的 **Sign plugin PR candidates** workflow：校验 candidate 字节 → 用 DBX Store 仓库密钥签名 → 发布签名后的 `.dbxp` 到对象存储 → 回写 `plugins/<id>.json` 与 `catalog/index.json` 到 PR。
- CI 变绿后由维护者合并；之后用户可在插件中心安装官方签名版本。
- 作者不持有、也不会收到 DBX Store 私钥；签名由商店完成。

## 5. 后续版本

- 每个新版本递增语义化版本，发布新的不可变 Release 资产，不覆盖旧资产。
- 已上架插件只需提交新的 `candidates/<plugin-id>.json`（省略未变化的 listing 字段）。
- 若仓库已登记到 `automation/plugin-sources.json`（`autoUpdate: true`），商店同步 workflow 会自动创建/更新候选 PR；否则按第 3 步手动提交。
