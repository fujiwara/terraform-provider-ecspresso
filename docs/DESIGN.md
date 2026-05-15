# terraform-provider-ecspresso 設計メモ

## 背景と動機

ECSのデプロイは [kayac/ecspresso](https://github.com/kayac/ecspresso) で管理し、ECSが依存するリソース(IAM Role, ALB target group, VPC など)はterraformで管理する、というのが現状のよくある構成。

この構成は、ECSサービス**に依存する**リソース(Application Auto Scaling, CodeDeploy deployment group など)もterraformで管理しようとすると、`terraform apply` → `ecspresso deploy` → `terraform apply` の **3段階構築**が必要になるという問題を抱えている。

現行のベストプラクティスはfujiwara氏が提案している [null_resource + local-exec provisioner](https://techblog.kayac.com/ecspresso-tf-nullresource) 方式で、これにより `terraform apply` 一撃で全体を構築できる。ただし以下の制約がある:

- `triggers` しか持たないため、ECSサービスの識別子(ARNなど)を他のリソースから参照しづらい
- destroy時の依存関係(特にCodeDeploy deployment group絡みで循環)が苦しく、aws-cli直叩きなどの回避策が必要 ([hrfmmrさんの記事](https://blog.hrfmmr.com/2023/01/16/terraform_with_ecs/))
- 既存ECSサービスのimportができない

これらを解消するために、専用の `terraform-provider-ecspresso` を作る。

## 既存調査

`terraform-provider-ecspresso` に該当する既存の公式/サードパーティProviderは(調査時点で)見つからなかった。

## 設計思想: tfは bootstrap、ecspresso CLI は日常運用

このProviderの**最大の動機は「terraform apply 一発で全体構築」を実現すること**であり、ecspressoの日常運用をterraformで行うためのものではない。

役割分担:

| 担当 | 範囲 |
|------|------|
| terraform (この Provider 経由) | 初回構築、依存リソース(IAM Role / ALB / VPC 等)が変わったときの再デプロイ、destroy |
| ecspresso CLI(アプリ開発者直接) | taskdef.json / service_def.json の編集を伴う日常デプロイ、image tag 更新、scale 操作 |

両者は同じ `ecspresso.yml` / `taskdef.json` / `service_def.json` を共有する。terraform は ecspresso の deploy ループに常時介入はしない。

### この役割分担から出る帰結

- terraform 側の redeploy は **terraform inputs の diff** だけで判定する(後述)
- `taskdef.json` / `service_def.json` の **ファイル内容を hash したりして tf state に持ち込まない**
- AWS 側の task definition revision は computed attribute として refresh で取り込むが、**tf input と突き合わせない**
- CLI で revision が何度進んでも、terraform apply は spurious redeploy を出さない

これは null_resource パターンが原理的に解けない問題で、専用 Provider にする一番の価値。

## 専用Providerにする価値

null_resource方式に対する優位点は主に4つ:

1. **computed attributesを露出できる**
   - `ecspresso_service.app.cluster_name` / `service_name` / `service_arn` / `task_definition_arn` を他リソースから直接参照可能
   - Application Auto Scalingの `resource_id` を文字列組み立てではなく参照で書ける

2. **destroy時の挙動を素直に書ける**
   - destroy時の挙動を `destroy_action` 属性で宣言(`delete` / `ignore` / `scale_zero_then_delete`)
   - 循環依存問題やaws-cli回避策をProvider側で吸収できる余地がある

3. **importできる**
   - 既存サービスをterraform stateに取り込める

4. **ecspressoをGoライブラリとして直接利用**
   - バイナリのパス解決・shell実行・stdout/stderrパースが不要
   - ログがterraformの構造化出力に乗る

## リソース設計

中核は1リソース `ecspresso_service`。

### Arguments

| 名前 | 必須 | 説明 |
|------|------|------|
| `config_path` | Required | ecspresso.yml へのパス |
| `tfstate_values` | Optional (map) | tfstate plugin への注入値(後述)。**redeploy のトリガ**でもある |
| `destroy_action` | Optional | `delete` (default) / `ignore` |

### `destroy_action` の値設計

aws_ecs_service と並べて整理:

| 値 | 中身 | aws_ecs_service 相当 |
|----|------|----------------------|
| `delete` (default) | scale to 0 + drain + DeleteService | `force_delete = false`(default、graceful) |
| `ignore` | tf state から消すだけ、AWS には触らない | (相当なし、本 Provider 固有) |

`ignore` は設計動機の CodeDeploy 循環依存問題に対する逃げ道。aws_ecs_service には無い、専用 Provider ならではの選択肢。

### ecspresso `DeleteOption` との対応(Phase 2 実装者向けメモ)

ecspresso の Delete API はこの構造体:

```go
type DeleteOption struct {
    DryRun    bool // dry-run
    Force     bool // CLI prompt を出さない(プロンプト抑制のみ、削除挙動には影響しない)
    Terminate bool // aws ecs delete-service --force 相当(scale to 0 + drain + DeleteService)
}
```

**用語が紛らわしい**: ecspresso の `Force` は「確認プロンプトを抑制する」だけで、AWS API の `force=true`(タスクが残っていても消す)ではない。AWS の `--force` 相当は **`Terminate: true`**。

Provider 経由(非対話)では `Force: true, Terminate: true` を渡すのが妥当。`destroy_action = "delete"` の実装はこれ。

`aws_ecs_service` の `force_delete = true`(タスクが残っていても消す)相当の挙動は ecspresso の API には無い(DeleteService に force=true を直接渡す経路は ecspresso 経由ではなく、必要なら別途 AWS SDK 直叩き)。緊急脱出が必要になった場合の追加検討事項。

**意図的に持たない属性:**

- `triggers`: null_resource 経験者は反射的に `triggers = { taskdef = filesha256(...) }` と書きがちだが、それを許すと「アプリ開発者が CLI で deploy → 別件 infra 変更で tf apply → spurious redeploy」が起きる。設計思想に反するので、API として最初から存在させない
- `envs`: ecspresso の `{{ env "..." }}` / `{{ must_env "..." }}` は OS 環境変数を読む。tf 側から渡す `envs` 属性を作るとプロセス env 汚染や並列実行 race など実装上の地雷が多く、用途も `tfstate_values` と大半が重複する。`IMAGE_TAG` のような日常変化値は ecspresso CLI 担当(設計思想)なので tf 側に持つ必要がない。初回 bootstrap で OS env が必要なケースは `IMAGE_TAG=v1 terraform apply` で対応
- `force_new_deployment`: ECS UpdateService の `forceNewDeployment` フラグ相当(SSM param 等を外部で書き換えた後の force-roll に使う)。これは典型的な日常運用ジェスチャで、ecspresso CLI(`ecspresso deploy --force-new-deployment`)で行うべき。tf 上で持つと「state に `true` が残り続けて気持ち悪い」「false に戻すとまた deploy 走る」という UX 問題があり、tfstate_values の diff があれば ecspresso が新リビジョン登録で勝手に rolling するので tf 経由 force の出番がほぼ無い
- `deploy_options` (`--no-wait` / `--latest-task-definition` / `--rollback-events` / `--suspend-auto-scaling` / `--resume-auto-scaling` の写像): それぞれ tf 経由 deploy で出番がない/合わない:
  - `--no-wait`: 初回 deploy 時に tf が先に終わって ECS service に依存する後続リソースが失敗する
  - `--latest-task-definition`: 強制 deploy ではなく、tfstate_values diff があれば taskdef も大抵変わる前提から外れる
  - `--rollback-events`: CodeDeploy 連携で使うが CodeDeploy は今後レガシー扱い
  - `--suspend/resume-auto-scaling`: 設計思想上 tf 経由 deploy は稀かつ計画的なので、autoscaling 暴れリスクはメンテ窓計画で吸収。さらに「deploy 設定の diff が redeploy を起こすべきか」という semantic 問題を抱える

「再 deploy したいが input は何も変えたくない」場合は ecspresso CLI で `ecspresso deploy --force-new-deployment` を使う。`terraform apply -replace=ecspresso_service.app` でも redeploy できるが destroy→create のためダウンタイムが出る。

### Computed attributes

- `id`(`<cluster>/<service>`)
- `service_arn`, `service_name`, `cluster_name`, `cluster_arn`
- `task_definition_arn`, `task_definition_family`, `task_definition_revision`

これらは Read のたびに AWS から refresh される。**ただし tf input との突き合わせは絶対にしない**(refresh で値が変わっても plan に diff として出ない `UseStateForUnknown` ＋ Computed-only)。

### redeploy 判定マトリクス

| 変化したもの | redeploy する? | 理由 |
|--------------|---------------|------|
| `tfstate_values` のいずれかの値 | yes | tf 管理リソースの変化を ecspresso 側に伝播する必要 |
| `config_path` | yes (recreate 寄り) | 別の service を指している可能性 |
| `taskdef.json` のファイル内容 | **no** | tf の関心外。ecspresso CLI 担当 |
| `service_def.json` のファイル内容 | **no** | 同上 |
| AWS 側の task definition revision | **no** | Read で computed attr に refresh するだけ |
| プロセス OS 環境変数 | **no** | ecspresso 内部で `env`/`must_env` が読むだけ、tf state に反映されない |

### ライフサイクル

- **Create**: `ecspresso deploy`(v2では新規/更新の区別なし)。computed attribute を AWS から取得して state に書く
- **Read**: DescribeServicesでACTIVE確認、computed更新、存在しなければ `RemoveResource` で再Create。**input(`tfstate_values` / `envs` / その他)を絶対に書き換えない**
- **Update**: tf-side input の diff が来たら deploy。AWS 側状態とは比較しない
- **Delete**: `destroy_action` に従う
- **ImportState**: ID形式は `<cluster>/<service>` が素直。configは別途.tfに書く

### 「ecspresso deployを単独で繰り返してもProviderは差分を出さない」の実現

設計思想からほぼ自動的に出る:

- Read では「サービスが存在するか」と「computed attributesの再取得」だけを行う
- input を一切リモート状態と突き合わせない
- input は plan 時の値と state に保存された前回値の比較だけで差分判定

リモートのタスク定義リビジョンが進んでも、tf-side input が変わらない限り diff は出ない。

## 中核論点: tfstate参照の解決

### 問題

ecspressoの設定ファイルはテンプレート記法で `{{ tfstate `aws_lb_target_group.app.arn` }}` のような記述ができ、CLI運用ではtfstateファイルを読んでリソースIDを解決する。

しかしProvider経由で実行する場合、tfstateファイルは原理的に**常に1ステップ遅れる**:

- 初回 `terraform apply` 時にはtfstateはまだ書かれていない
- 2回目以降も、apply完了までtfstateには反映されない(同一apply中の変更値は引けない)

これはProvider実装の問題ではなく、tfstateというデータソースの性質。

### 解決方針: tfstate pluginに「Provider経由のときは外部注入を受け入れる」モードを追加

ecspressoのpluginはfuncmapを定義するもの(tfstate pluginを定義すると `tfstate` 関数が生える)。tfstate pluginのオプションに `from_provider` フラグを追加し、Provider経由のときだけ外部からの値注入を許可する。

#### ecspresso.yml

```yaml
plugins:
  - name: tfstate
    config:
      path: ../terraform.tfstate   # CLI実行時のソース
      from_provider: true          # Provider実行時は注入を受け付ける
```

`from_provider` は注入を**許可**するだけで強制ではない。CLIから `ecspresso deploy` を叩いたときは注入元が存在しないので、普通にファイルから読む。Provider経由で呼ばれたときは、注入値を優先し、無いキーはファイルにフォールバック。

**同じecspresso.ymlがCLI / Provider両対応** になるのがポイント。

#### terraform側

```hcl
resource "ecspresso_service" "app" {
  config_path = "./ecspresso.yml"

  tfstate_values = {
    "aws_lb_target_group.app.arn" = aws_lb_target_group.app.arn
    "aws_iam_role.task.arn"       = aws_iam_role.task.arn
    "aws_ecs_cluster.main.name"   = aws_ecs_cluster.main.name
    "aws_security_group.app.id"   = aws_security_group.app.id
  }

  envs = {
    IMAGE_TAG = var.image_tag
  }
}
```

terraformのグラフでこの参照が依存解決されるため、tfstateファイルを介さない。「1ステップ遅れ」問題が原理的に発生しない。

#### 優先順位

1. injection map (Providerから注入された値)
2. path指定のtfstate file (fallback)
3. エラー

`path` は `from_provider: true` のとき optional にする。

### フォールバックを残す理由

「ネットワーク系は別tfstate」のような構成への配慮。Providerが渡せるのは同じtfconfig内のリソースだけなので、別tfstate由来のリソースID(VPC・サブネット等)は引き続きファイル参照で解決させたい。

## ecspresso本体への変更

### tfstate pluginの拡張

- `from_provider` configオプションを追加
- pluginが「外部注入チャネル」を自分のクロージャに保持
- `tfstate` / `tfstate_lookup` / `tfstatef` の3関数すべてが同じinjection mapを参照(同じクロージャ変数を共有)

### 注入APIの選択肢

- **メソッド方式**: `app.SetTFStateValues(map[string]string)` のような明示的メソッド
  - ecspresso内部で `context.Context` を引き回す改修が不要で軽い
- **context方式**: `context.Context` にキーを仕込んでplugin funcの中で `ctx.Value` で引く
  - Goらしいが、ecspresso全体で `ctx` を運ぶ必要

fujiwara氏と相談する価値のある分岐点。

### `from_provider: true` なのにProvider外から起動されたとき

- `path` があればファイルfallback
- 無ければ「injection mapが空かつpath未指定」でエラー
- 「うっかりCLIで叩いたら未指定値で動いた」を防げる

## 残る論点

### 1. valuesに書き漏らすと実行時エラー

ecspresso.yml内のすべてのtfstate参照を網羅して `tfstate_values` に書く必要があり、漏れるとapply実行中に「key not found」で落ちる。

→ ecspresso.ymlをパースしてtfstate参照を抽出し、不足キーをvalidate段階で警告する補助ロジックがほしい。

### 2. 強制redeployのUX

「タスク定義は変えてないがForce new deploymentしたい」ケース:

- 第一選択: `ecspresso deploy --force-new-deployment` を CLI で叩く(日常運用領分)
- 緊急時の tf-native 手段: `terraform apply -replace=ecspresso_service.app`(ただし destroy→create でダウンタイム発生)

`triggers` / `force_new_deployment` は持たない方針(Arguments セクション参照)。tf 経由のノーダウンタイム force-roll は意図的にサポート外。

### 3. ecspressoバージョンの整合性

Providerに同梱するecspressoバージョンと、開発者ローカルのCLIバージョンが乖離するとデプロイ挙動が微妙にずれる可能性。

→ Providerバージョンと対応ecspressoバージョンを明示する運用が必要。

### 4. 並列applyとの相性

同一クラスタの複数サービスをterraformが並列applyすると、ecspresso内部のAWS API呼び出しがスロットリングに当たりやすい。

→ `-parallelism` の制御を意識する必要。

### 5. CodeDeploy controllerでのdestroy順序

`aws_codedeploy_deployment_group` との依存グラフが循環しないよう、Provider側で「ecspresso経由でscale 0 → service delete してから deployment group削除」を吸収する設計を検討。あるいは `destroy_action = "ignore"` で逃がしてもらう。

### 6. import時の整合性

既存サービスをimportした直後、`tfstate_values` の参照先リソースもまだstateに入っていない可能性。

→ ImportStateでは値の解決はせず、次のplanで `tfstate_values` の妥当性が確認される流れ。

### 7. `from_provider` という命名の汎用性

特定ユースケースに寄った名前。汎用化するなら `accept_external_values` / `external_lookup` の方が筋がよいかもしれない。

ただしユースケースが明確なので `from_provider` のままでも通る可能性は十分ある(fujiwara氏自身がterraform連携をブログ記事にしているくらいなので)。

## 進め方

ecspresso本体への変更とProvider実装を切り離して進められる:

1. **ecspresso本体** に `tfstate plugin: from_provider` オプションと注入APIを追加するPR
2. それがマージされた依存バージョンを使う形でProvider側を実装

1のPRが小さく独立しているので、議論もしやすい。

## Phase 進捗

- [x] **Phase 0**: テンプレ scaffolding の片付け(`cmd/FIXME` 削除、`main.go` を providerserver 化、Makefile / .goreleaser.yml を Terraform Provider 配布規約に合わせる、README 実体化)
- [x] **Phase 1**: Plugin Framework での Provider 骨組み(`provider.ecspresso`、`ecspresso_service` resource のフル schema、CRUD は "not implemented" diagnostic を返すスタブ)
- [x] **Phase 2**: Create / Read / Update / Delete の実装(`ecspresso v2` を Go ライブラリとして `internal/ecspressoapi` 経由で薄くラップ。`tfstate_values` は schema で受け取るが本体側未対応なので未注入。`config_path` は RequiresReplace。`destroy_action = "ignore"` は AWS に触らず state からのみ削除)
- [ ] **Phase 3**: `tfstate_values` 配線のための周辺整備(本体 PR 待ち)、Update の diff 判定改善(`destroy_action` 変更だけのときは deploy しない、など)
- [ ] **Phase 4** (A 系統 / 別リポジトリ): ecspresso 本体に `from_provider` オプションと注入 API を追加する PR
- [ ] **Phase 5**: `tfstate_values` 配線(A 系統マージ後)、ecspresso.yml をパースして不足キーを警告する補助ロジック
- [ ] **Phase 6**: destroy_action の `scale_zero_then_delete` 検証、acceptance test 整備、README 拡充

## TODO

- [ ] ecspresso本体の `plugin_tfstate.go` 周辺のコード構造を確認し、注入の現実的な実装位置を特定する
- [ ] ecspresso本体に外部から関数差し替え/値注入できるpublic APIがあるか確認
- [ ] fujiwara氏に方針相談(注入API設計、命名)
