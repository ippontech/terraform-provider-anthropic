## [1.25.4](https://github.com/ippontech/terraform-provider-anthropic/compare/v1.25.3...v1.25.4) (2026-06-23)

### 🐛 Bug Fixes

* **agents:** use jsontypes.Normalized for custom_tool input_schema ([#150](https://github.com/ippontech/terraform-provider-anthropic/issues/150)) ([9f7ef38](https://github.com/ippontech/terraform-provider-anthropic/commit/9f7ef384924b7cd339d26ecffd826c1fb62d3408)), closes [#133](https://github.com/ippontech/terraform-provider-anthropic/issues/133)

## [1.25.3](https://github.com/ippontech/terraform-provider-anthropic/compare/v1.25.2...v1.25.3) (2026-06-23)

### 🐛 Bug Fixes

* **deps:** update go dependencies ([#133](https://github.com/ippontech/terraform-provider-anthropic/issues/133)) ([4fe8c7e](https://github.com/ippontech/terraform-provider-anthropic/commit/4fe8c7e2313c4c94a22e216292d7fbfe496f1c13))

## [1.25.2](https://github.com/ippontech/terraform-provider-anthropic/compare/v1.25.1...v1.25.2) (2026-06-19)

### 📚 Documentation

* clarify commit signing is optional for contributors ([#138](https://github.com/ippontech/terraform-provider-anthropic/issues/138)) ([28f82ca](https://github.com/ippontech/terraform-provider-anthropic/commit/28f82caeaafc6ac44aaeec1b7f7ed73d9a36f837))

## [1.25.1](https://github.com/ippontech/terraform-provider-anthropic/compare/v1.25.0...v1.25.1) (2026-06-18)

### 🐛 Bug Fixes

* **skills:** preserve nested subdirectories in bundle uploads ([#136](https://github.com/ippontech/terraform-provider-anthropic/issues/136)) ([38b4a8f](https://github.com/ippontech/terraform-provider-anthropic/commit/38b4a8f2754d94b79d3b452a018cbfc2b0978c63))

## [1.25.0](https://github.com/ippontech/terraform-provider-anthropic/compare/v1.24.0...v1.25.0) (2026-05-27)

### ✨ Features

* **apikeys:** add anthropic_api_key and anthropic_api_keys data sources ([#131](https://github.com/ippontech/terraform-provider-anthropic/issues/131)) ([243feca](https://github.com/ippontech/terraform-provider-anthropic/commit/243fecafee7585d1aca08745559aa3a1461d9313))

## [1.24.0](https://github.com/ippontech/terraform-provider-anthropic/compare/v1.23.1...v1.24.0) (2026-05-27)

### ✨ Features

* **apikeys:** add anthropic_api_key resource (update + delete + import only) ([#130](https://github.com/ippontech/terraform-provider-anthropic/issues/130)) ([56fc711](https://github.com/ippontech/terraform-provider-anthropic/commit/56fc71129d35120148039d8a4225df4a904aaee7))

## [1.23.1](https://github.com/ippontech/terraform-provider-anthropic/compare/v1.23.0...v1.23.1) (2026-05-27)

### 📚 Documentation

* standardize API/Auth/Beta header/Cost block across all resource and data source docs ([#127](https://github.com/ippontech/terraform-provider-anthropic/issues/127)) ([b5c0571](https://github.com/ippontech/terraform-provider-anthropic/commit/b5c0571e2475d78699de26f858397dec5417ec1d)), closes [#78](https://github.com/ippontech/terraform-provider-anthropic/issues/78)

## [1.23.0](https://github.com/ippontech/terraform-provider-anthropic/compare/v1.22.0...v1.23.0) (2026-05-13)

### ✨ Features

* **environments:** add archive_on_destroy to anthropic_environment resource ([#124](https://github.com/ippontech/terraform-provider-anthropic/issues/124)) ([10dfe43](https://github.com/ippontech/terraform-provider-anthropic/commit/10dfe439c3b54077b047285492c917158b2e221e)), closes [#73](https://github.com/ippontech/terraform-provider-anthropic/issues/73) [#123](https://github.com/ippontech/terraform-provider-anthropic/issues/123)

## [1.22.0](https://github.com/ippontech/terraform-provider-anthropic/compare/v1.21.0...v1.22.0) (2026-05-06)

### ✨ Features

* **workspaces:** add anthropic_workspace_rate_limits data source ([#110](https://github.com/ippontech/terraform-provider-anthropic/issues/110)) ([c3db0cc](https://github.com/ippontech/terraform-provider-anthropic/commit/c3db0cc1e47255377e2073052e1f2b21f280601a))

## [1.21.0](https://github.com/ippontech/terraform-provider-anthropic/compare/v1.20.1...v1.21.0) (2026-05-06)

### ✨ Features

* **workspaces:** add anthropic_workspace_member resource ([#109](https://github.com/ippontech/terraform-provider-anthropic/issues/109)) ([86b57a2](https://github.com/ippontech/terraform-provider-anthropic/commit/86b57a2b7274d6d2985c86c4c90614984e1d222f)), closes [#54](https://github.com/ippontech/terraform-provider-anthropic/issues/54) [#58](https://github.com/ippontech/terraform-provider-anthropic/issues/58)

## [1.20.1](https://github.com/ippontech/terraform-provider-anthropic/compare/v1.20.0...v1.20.1) (2026-05-06)

### ♻️ Refactoring

* **workspaces:** clean up example folder names and deduplicate test helpers ([#114](https://github.com/ippontech/terraform-provider-anthropic/issues/114)) ([ccdd78c](https://github.com/ippontech/terraform-provider-anthropic/commit/ccdd78c75e15808cca2206c881c8991cec8ecb38))

## [1.20.0](https://github.com/ippontech/terraform-provider-anthropic/compare/v1.19.0...v1.20.0) (2026-05-06)

### ✨ Features

* **workspaces:** add anthropic_workspace_members data source ([#108](https://github.com/ippontech/terraform-provider-anthropic/issues/108)) ([5d2312b](https://github.com/ippontech/terraform-provider-anthropic/commit/5d2312b12658ecf856da941f4d36761b9fb34f50)), closes [#56](https://github.com/ippontech/terraform-provider-anthropic/issues/56)

## [1.19.0](https://github.com/ippontech/terraform-provider-anthropic/compare/v1.18.1...v1.19.0) (2026-05-06)

### ✨ Features

* **workspaces:** add anthropic_workspace_member data source ([#107](https://github.com/ippontech/terraform-provider-anthropic/issues/107)) ([f07e5ac](https://github.com/ippontech/terraform-provider-anthropic/commit/f07e5ac3f367459d14bd70cfb9db453cdf26e09b)), closes [#113](https://github.com/ippontech/terraform-provider-anthropic/issues/113) [#58](https://github.com/ippontech/terraform-provider-anthropic/issues/58) [#55](https://github.com/ippontech/terraform-provider-anthropic/issues/55) [#113](https://github.com/ippontech/terraform-provider-anthropic/issues/113) [#113](https://github.com/ippontech/terraform-provider-anthropic/issues/113)

## [1.18.1](https://github.com/ippontech/terraform-provider-anthropic/compare/v1.18.0...v1.18.1) (2026-05-05)

### 🐛 Bug Fixes

* **tests:** remove unevaluable asserts from workspace data source plan test ([#112](https://github.com/ippontech/terraform-provider-anthropic/issues/112)) ([aa9e2b6](https://github.com/ippontech/terraform-provider-anthropic/commit/aa9e2b6504b261bc9787e0595db0d39a5d935b23))

## [1.18.0](https://github.com/ippontech/terraform-provider-anthropic/compare/v1.17.0...v1.18.0) (2026-05-05)

### ✨ Features

* **workspaces:** add anthropic_workspace data source ([#105](https://github.com/ippontech/terraform-provider-anthropic/issues/105)) ([ad1d584](https://github.com/ippontech/terraform-provider-anthropic/commit/ad1d584cdc13c7481c54bb11c0c55d479a937c89)), closes [#52](https://github.com/ippontech/terraform-provider-anthropic/issues/52)

## [1.17.0](https://github.com/ippontech/terraform-provider-anthropic/compare/v1.16.2...v1.17.0) (2026-05-05)

### ✨ Features

* **workspaces:** add anthropic_workspaces data source ([#106](https://github.com/ippontech/terraform-provider-anthropic/issues/106)) ([0256b08](https://github.com/ippontech/terraform-provider-anthropic/commit/0256b082fe58aaf69428b92ab438e150eb25592e))

## [1.16.2](https://github.com/ippontech/terraform-provider-anthropic/compare/v1.16.1...v1.16.2) (2026-05-05)

### ♻️ Refactoring

* reorganize resources and data sources by service ([#104](https://github.com/ippontech/terraform-provider-anthropic/issues/104)) ([43f3da2](https://github.com/ippontech/terraform-provider-anthropic/commit/43f3da25d6bf7ce74389ac312f94a1cf6bd4167f))

## [1.16.1](https://github.com/ippontech/terraform-provider-anthropic/compare/v1.16.0...v1.16.1) (2026-05-05)

### ♻️ Refactoring

* **docs:** rename subcategory "Agents" to "Managed Agents" ([#103](https://github.com/ippontech/terraform-provider-anthropic/issues/103)) ([44dbe1e](https://github.com/ippontech/terraform-provider-anthropic/commit/44dbe1e8eb948ddc9c72444776367c8f24315819)), closes [#90](https://github.com/ippontech/terraform-provider-anthropic/issues/90)

## [1.16.0](https://github.com/ippontech/terraform-provider-anthropic/compare/v1.15.0...v1.16.0) (2026-05-04)

### ✨ Features

* add anthropic_environments data source ([#72](https://github.com/ippontech/terraform-provider-anthropic/issues/72)) ([3b6b452](https://github.com/ippontech/terraform-provider-anthropic/commit/3b6b452ffb88ed68d637473697b087d12e4d91ab)), closes [#68](https://github.com/ippontech/terraform-provider-anthropic/issues/68)

## [1.15.0](https://github.com/ippontech/terraform-provider-anthropic/compare/v1.14.3...v1.15.0) (2026-05-04)

### ✨ Features

* add anthropic_environment data source ([#71](https://github.com/ippontech/terraform-provider-anthropic/issues/71)) ([aad7b7e](https://github.com/ippontech/terraform-provider-anthropic/commit/aad7b7e3fbb0bfaf87067acb2331cc9d59a6f4c9)), closes [#67](https://github.com/ippontech/terraform-provider-anthropic/issues/67)

## [1.14.3](https://github.com/ippontech/terraform-provider-anthropic/compare/v1.14.2...v1.14.3) (2026-05-04)

### ♻️ Refactoring

* **retry:** extract multipart upload retry into internal/retry ([#99](https://github.com/ippontech/terraform-provider-anthropic/issues/99)) ([324a3f2](https://github.com/ippontech/terraform-provider-anthropic/commit/324a3f27cf7ca94b8da5a86dcd4a466b5e56d41e))

## [1.14.2](https://github.com/ippontech/terraform-provider-anthropic/compare/v1.14.1...v1.14.2) (2026-05-04)

### 🐛 Bug Fixes

* **environment:** add plan modifiers to resolve acc test failures ([#98](https://github.com/ippontech/terraform-provider-anthropic/issues/98)) ([24312d7](https://github.com/ippontech/terraform-provider-anthropic/commit/24312d7f3fda0792c29679b0e7a0821575fb1bc9))

## [1.14.1](https://github.com/ippontech/terraform-provider-anthropic/compare/v1.14.0...v1.14.1) (2026-05-04)

### ♻️ Refactoring

* make API keys optional, extract providerErrors helpers, and improve Claude config ([#96](https://github.com/ippontech/terraform-provider-anthropic/issues/96)) ([54332be](https://github.com/ippontech/terraform-provider-anthropic/commit/54332be90daa06fed69faf0ee674a73025bdac1b))

## [1.14.0](https://github.com/ippontech/terraform-provider-anthropic/compare/v1.13.5...v1.14.0) (2026-05-04)

### ✨ Features

* add anthropic_environment resource ([#70](https://github.com/ippontech/terraform-provider-anthropic/issues/70)) ([af4690c](https://github.com/ippontech/terraform-provider-anthropic/commit/af4690c2dbd9e190fc3201e1321e83a4a7235be1)), closes [#66](https://github.com/ippontech/terraform-provider-anthropic/issues/66)

## [1.13.5](https://github.com/ippontech/terraform-provider-anthropic/compare/v1.13.4...v1.13.5) (2026-04-28)

### 🐛 Bug Fixes

* delete skill versions before deleting skill, fix import state ([#65](https://github.com/ippontech/terraform-provider-anthropic/issues/65)) ([bf8992d](https://github.com/ippontech/terraform-provider-anthropic/commit/bf8992dbfd4ba52fa63ada183c6b6593cb52bb97))

## [1.13.4](https://github.com/ippontech/terraform-provider-anthropic/compare/v1.13.3...v1.13.4) (2026-04-27)

### 🐛 Bug Fixes

* align SKILL.md name field with parent directory name ([#64](https://github.com/ippontech/terraform-provider-anthropic/issues/64)) ([a9f28a8](https://github.com/ippontech/terraform-provider-anthropic/commit/a9f28a8b07529c5d7668e0a6a3f9ae1696f50b75))

## [1.13.3](https://github.com/ippontech/terraform-provider-anthropic/compare/v1.13.2...v1.13.3) (2026-04-27)

### 🐛 Bug Fixes

* add YAML frontmatter to SKILL.md example and test files ([#63](https://github.com/ippontech/terraform-provider-anthropic/issues/63)) ([f1fdce0](https://github.com/ippontech/terraform-provider-anthropic/commit/f1fdce0456a99a8bb058bc09c4f1ce671c90e1bf))

## [1.13.2](https://github.com/ippontech/terraform-provider-anthropic/compare/v1.13.1...v1.13.2) (2026-04-25)

### 🐛 Bug Fixes

* prefix skill file uploads with parent directory name ([#60](https://github.com/ippontech/terraform-provider-anthropic/issues/60)) ([0405f4c](https://github.com/ippontech/terraform-provider-anthropic/commit/0405f4c2f294fbb2999bea59875b6df6a47a75ec))

## [1.13.1](https://github.com/ippontech/terraform-provider-anthropic/compare/v1.13.0...v1.13.1) (2026-04-25)

### 🐛 Bug Fixes

* **deps:** update go dependencies ([#17](https://github.com/ippontech/terraform-provider-anthropic/issues/17)) ([eae37c1](https://github.com/ippontech/terraform-provider-anthropic/commit/eae37c15bc14ae110f829c40c051fb1cd15fe42f))

## [1.13.0](https://github.com/ippontech/terraform-provider-anthropic/compare/v1.12.0...v1.13.0) (2026-04-25)

### ✨ Features

* add anthropic_workspace resource ([#59](https://github.com/ippontech/terraform-provider-anthropic/issues/59)) ([cc948fc](https://github.com/ippontech/terraform-provider-anthropic/commit/cc948fc60ab4b7833d0299534ae6515f979010ae)), closes [#58](https://github.com/ippontech/terraform-provider-anthropic/issues/58)

## [1.12.0](https://github.com/ippontech/terraform-provider-anthropic/compare/v1.11.0...v1.12.0) (2026-04-22)

### ✨ Features

* add anthropic_skill_version data source ([#48](https://github.com/ippontech/terraform-provider-anthropic/issues/48)) ([b861b97](https://github.com/ippontech/terraform-provider-anthropic/commit/b861b976ef67bef84fa55d7b43edccb4f6175e9d)), closes [#46](https://github.com/ippontech/terraform-provider-anthropic/issues/46) [#36](https://github.com/ippontech/terraform-provider-anthropic/issues/36)

## [1.11.0](https://github.com/ippontech/terraform-provider-anthropic/compare/v1.10.0...v1.11.0) (2026-04-22)

### ✨ Features

* add anthropic_skill_versions data source ([#47](https://github.com/ippontech/terraform-provider-anthropic/issues/47)) ([dea5026](https://github.com/ippontech/terraform-provider-anthropic/commit/dea50261f5dd736774b54a5ebcf9da82c5b9c207)), closes [#46](https://github.com/ippontech/terraform-provider-anthropic/issues/46) [#37](https://github.com/ippontech/terraform-provider-anthropic/issues/37)

## [1.10.0](https://github.com/ippontech/terraform-provider-anthropic/compare/v1.9.0...v1.10.0) (2026-04-22)

### ✨ Features

* implement anthropic_skills data source ([#45](https://github.com/ippontech/terraform-provider-anthropic/issues/45)) ([4543f19](https://github.com/ippontech/terraform-provider-anthropic/commit/4543f199373a65980b7d7368d8c6fb51ccf9c0a9)), closes [#43](https://github.com/ippontech/terraform-provider-anthropic/issues/43)

## [1.9.0](https://github.com/ippontech/terraform-provider-anthropic/compare/v1.8.0...v1.9.0) (2026-04-22)

### ✨ Features

* implement anthropic_skill_version resource ([#46](https://github.com/ippontech/terraform-provider-anthropic/issues/46)) ([6ad3980](https://github.com/ippontech/terraform-provider-anthropic/commit/6ad39802ecf113568f27b6c0f87db0755d840cf1)), closes [#43](https://github.com/ippontech/terraform-provider-anthropic/issues/43)

## [1.8.0](https://github.com/ippontech/terraform-provider-anthropic/compare/v1.7.0...v1.8.0) (2026-04-22)

### ✨ Features

* implement anthropic_skill data source ([#44](https://github.com/ippontech/terraform-provider-anthropic/issues/44)) ([73a85d3](https://github.com/ippontech/terraform-provider-anthropic/commit/73a85d3b91ce1de9c7f291970b0ed0380a9dea02)), closes [#43](https://github.com/ippontech/terraform-provider-anthropic/issues/43)

## [1.7.0](https://github.com/ippontech/terraform-provider-anthropic/compare/v1.6.2...v1.7.0) (2026-04-22)

### ✨ Features

* implement anthropic_skill resource ([#43](https://github.com/ippontech/terraform-provider-anthropic/issues/43)) ([ec6c9d5](https://github.com/ippontech/terraform-provider-anthropic/commit/ec6c9d59ce7774853758c2234f06ec384d5bfa8a))

## [1.6.2](https://github.com/ippontech/terraform-provider-anthropic/compare/v1.6.1...v1.6.2) (2026-04-20)

### 📚 Documentation

* add Agents subcategory, enforce template convention, ignore worktrees ([#31](https://github.com/ippontech/terraform-provider-anthropic/issues/31)) ([a77c683](https://github.com/ippontech/terraform-provider-anthropic/commit/a77c6838d59ea504bbedf94a7936a83ad4a46a5f))

## [1.6.1](https://github.com/ippontech/terraform-provider-anthropic/compare/v1.6.0...v1.6.1) (2026-04-20)

### 🐛 Bug Fixes

* pre-install Terraform in testacc job to bypass expired GPG key ([#30](https://github.com/ippontech/terraform-provider-anthropic/issues/30)) ([1aac9de](https://github.com/ippontech/terraform-provider-anthropic/commit/1aac9de704bc5bb6a8d952fba2d6deff18282f71))

## [1.6.0](https://github.com/ippontech/terraform-provider-anthropic/compare/v1.5.0...v1.6.0) (2026-04-20)

### ✨ Features

* add anthropic_agents data source ([#29](https://github.com/ippontech/terraform-provider-anthropic/issues/29)) ([9a58c38](https://github.com/ippontech/terraform-provider-anthropic/commit/9a58c384099800d259f76087dc8ab8dba3ec2966)), closes [#27](https://github.com/ippontech/terraform-provider-anthropic/issues/27)

## [1.5.0](https://github.com/ippontech/terraform-provider-anthropic/compare/v1.4.3...v1.5.0) (2026-04-20)

### ✨ Features

* add anthropic_agent data source ([#28](https://github.com/ippontech/terraform-provider-anthropic/issues/28)) ([b53b638](https://github.com/ippontech/terraform-provider-anthropic/commit/b53b638a56a280bb8090b51df86ea2297e00a912))

## [1.4.3](https://github.com/ippontech/terraform-provider-anthropic/compare/v1.4.2...v1.4.3) (2026-04-20)

### 🐛 Bug Fixes

* remove Stop hook to prevent vibe-kanban infinite loop ([#25](https://github.com/ippontech/terraform-provider-anthropic/issues/25)) ([da4bb99](https://github.com/ippontech/terraform-provider-anthropic/commit/da4bb99385bdd49f8487db798d23f6bc7d70909b))

## [1.4.2](https://github.com/ippontech/terraform-provider-anthropic/compare/v1.4.1...v1.4.2) (2026-04-20)

### 📚 Documentation

* add make default reminder after feature/bug fix implementation ([#24](https://github.com/ippontech/terraform-provider-anthropic/issues/24)) ([5f4275e](https://github.com/ippontech/terraform-provider-anthropic/commit/5f4275ef8c99128f6929cb871cc31647627272c1))

## [1.4.1](https://github.com/ippontech/terraform-provider-anthropic/compare/v1.4.0...v1.4.1) (2026-04-20)

### 📚 Documentation

* enrich CLAUDE.md with architecture-first layout and Terraform tests ([#23](https://github.com/ippontech/terraform-provider-anthropic/issues/23)) ([ea62cc2](https://github.com/ippontech/terraform-provider-anthropic/commit/ea62cc2d588b3d8f04f21c0d5405bc9770b2ec84))

## [1.4.0](https://github.com/ippontech/terraform-provider-anthropic/compare/v1.3.0...v1.4.0) (2026-04-15)

### ✨ Features

* add anthropic_agent resource for Managed Agents ([#19](https://github.com/ippontech/terraform-provider-anthropic/issues/19)) ([f57ec94](https://github.com/ippontech/terraform-provider-anthropic/commit/f57ec94ae60db301058c5613b9713847d27687d3))

## [1.3.0](https://github.com/ippontech/terraform-provider-anthropic/compare/v1.2.0...v1.3.0) (2026-04-15)

### ✨ Features

* add anthropic_model data source ([#10](https://github.com/ippontech/terraform-provider-anthropic/issues/10)) ([5fe9723](https://github.com/ippontech/terraform-provider-anthropic/commit/5fe9723cae93e4c5e394ef4f9061b1b686f0a5d3))

## [1.2.0](https://github.com/ippontech/terraform-provider-anthropic/compare/v1.1.1...v1.2.0) (2026-04-13)

### ✨ Features

* add anthropic_count_tokens data source ([#8](https://github.com/ippontech/terraform-provider-anthropic/issues/8)) ([790b7a2](https://github.com/ippontech/terraform-provider-anthropic/commit/790b7a2ff376bc20def47dfcc3ee9df12859683d))

## [1.1.1](https://github.com/ippontech/terraform-provider-anthropic/compare/v1.1.0...v1.1.1) (2026-04-13)

### 🐛 Bug Fixes

* commit missing generated docs from [#6](https://github.com/ippontech/terraform-provider-anthropic/issues/6) ([#7](https://github.com/ippontech/terraform-provider-anthropic/issues/7)) ([2ff0843](https://github.com/ippontech/terraform-provider-anthropic/commit/2ff08430434c381a219e52fa9f3b23d78bb1743b))

## [1.1.0](https://github.com/ippontech/terraform-provider-anthropic/compare/v1.0.1...v1.1.0) (2026-04-13)

### ✨ Features

* add anthropic_message resource ([#6](https://github.com/ippontech/terraform-provider-anthropic/issues/6)) ([1563330](https://github.com/ippontech/terraform-provider-anthropic/commit/1563330277ba73a124ff6f1460cf3150d69bdbd0))

## [1.0.1](https://github.com/ippontech/terraform-provider-anthropic/compare/v1.0.0...v1.0.1) (2026-04-13)

### 🐛 Bug Fixes

* update registry address and add Terraform tests on examples ([#5](https://github.com/ippontech/terraform-provider-anthropic/issues/5)) ([b48790d](https://github.com/ippontech/terraform-provider-anthropic/commit/b48790d5fb9b21387a399882b164e61a0f34fc21))

## 1.0.0 (2026-04-10)

### ✨ Features

* add anthropic_models datasource ([#3](https://github.com/ippontech/terraform-provider-anthropic/issues/3)) ([b7d3876](https://github.com/ippontech/terraform-provider-anthropic/commit/b7d3876b3c82c3bb5768cf595b1dd45983b60e90))
* initialize Anthropic Terraform provider ([#1](https://github.com/ippontech/terraform-provider-anthropic/issues/1)) ([b26c8f0](https://github.com/ippontech/terraform-provider-anthropic/commit/b26c8f0c4f82fde987fe8d97c01d367e994f2df3))
