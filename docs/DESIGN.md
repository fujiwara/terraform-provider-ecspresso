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
- AWS 側の task definition revision は **computed attribute としても露出しない**(CLI で常に進む値で、tf が authoritative に保てない)
- CLI で revision が何度進んでも、terraform apply は spurious redeploy を出さない

これは null_resource パターンが原理的に解けない問題で、専用 Provider にする一番の価値。

## 専用Providerにする価値

null_resource方式に対する優位点は主に4つ:

1. **computed attributesを露出できる**
   - `ecspresso_service.app.cluster_name` / `service_name` / `service_arn` / `cluster_arn` を他リソースから直接参照可能
   - Application Auto Scalingの `resource_id` を文字列組み立てではなく参照で書ける
   - task definition系の値(arn/family/revision)はあえて露出しない。ecspresso CLIでdeployするたびに進む値なのでtf側からは常に陳腐化し、authoritativeに保てない

2. **destroy時の挙動を素直に書ける**
   - destroy時の挙動を `destroy_action` 属性で宣言(`delete` / `ignore` / `scale_zero_then_delete`)
   - 循環依存問題やaws-cli回避策をProvider側で吸収できる余地がある

3. **既存サービスをterraform管理下に取り込める**
   - 仕組みは `terraform import` ではなく「初回 apply による取り込み」。`ecspresso deploy` がidempotentなので、既存サービスを指す `ecspresso.yml` を `config_path` に指定して apply するだけで安全に state 化できる。詳細は README の "Adopting an existing ECS service" を参照
   - `terraform import` は実装しない。リソースの実体は `ecspresso.yml`(およびtask/service definition templates) であって cluster/service 名のペアではないため、importのidentifierだけで残りの属性 (`config_path`, `tfstate_values`, `tfstate_func_prefix`, `destroy_action`) を復元できず、結局 `.tf` 側を書く手間は変わらない

4. **ecspressoをGoライブラリとして直接利用**
   - バイナリのパス解決・shell実行・stdout/stderrパースが不要
   - ログがterraformの構造化出力に乗る

## リソース設計

中核は1リソース `ecspresso_service`。

### Arguments

| 名前 | 必須 | 説明 |
|------|------|------|
| `config_path` | Required | ecspresso.yml へのパス。**相対パスは `.tf` のあるディレクトリではなく `terraform` プロセスの CWD 基準**(Terraform は属性文字列を rewrite せず、provider 側でモジュールパスを知る手段はないため)。安全側のイディオムは `"${path.module}/ecspresso.yml"`。`-chdir` や child module 化で壊れにくくなる |
| `tfstate_values` | Optional (string; JSON object) | resource 単位の tfstate 上書き値(後述)。**redeploy のトリガ**でもある。`jsonencode({...})` で渡す JSON オブジェクト文字列。文字列にしている理由は 1b 節参照(初回構築の inconsistent final plan 回避) |
| `tfstate_func_prefix` | Optional (string, default `""`) | 上書きを適用する tfstate plugin の指定。ecspresso 設定で複数の tfstate plugin を `func_prefix` で区別している場合に対象を選ぶ。1 個しか tfstate plugin が無い大半のケースでは指定不要 |
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

これらは作成後は不変な identifier 系のみ。Read のたびに AWS から refresh されるが `UseStateForUnknown` で plan に diff としては出ない。

`task_definition_arn` / `task_definition_family` / `task_definition_revision` および desired count などの「AWS 側が握っていて頻繁に動く値」は **意図的に露出しない**。ecspresso CLI deploy で毎回 revision が進む値で、tf state は常に最後の `terraform apply` 時点で凍結されてしまう。露出すると「依存させていい computed」という誤解を招き、CLI deploy のたびに refresh で値が動いて意味のない更新の連鎖や drift 表示を生む。

これらが tf 上で必要なら `data "aws_ecs_service"` を `ecspresso_service` の `cluster_arn` / `service_name` で繋ぐ。data source は plan のたびに再 read されるので、CLI deploy が何度走ったあとでも常に最新値が引ける(リソース参照で暗黙の依存ができるので `depends_on` も不要):

```hcl
data "aws_ecs_service" "app" {
  service_name = ecspresso_service.app.service_name
  cluster_arn  = ecspresso_service.app.cluster_arn
}
# data.aws_ecs_service.app.task_definition ...
```

provider が `service_name` / `cluster_arn` を computed で出している主目的のひとつがこのパターンへの橋渡し。「現在値を知りたい」のニーズは data source 側に逃がし、provider 自身は authoritative に保てる identifier だけを持つ、という分担。

### redeploy 判定マトリクス

| 変化したもの | redeploy する? | 理由 |
|--------------|---------------|------|
| `tfstate_values` のいずれかの値 | yes | tf 管理リソースの変化を ecspresso 側に伝播する必要 |
| `tfstate_func_prefix` | yes | ターゲットの tfstate plugin が変わる |
| `config_path` | yes (recreate 寄り) | 別の service を指している可能性 |
| `taskdef.json` のファイル内容 | **no** | tf の関心外。ecspresso CLI 担当 |
| `service_def.json` のファイル内容 | **no** | 同上 |
| AWS 側の task definition revision | **no** | tf からは観測しない(computed にも露出させない) |
| プロセス OS 環境変数 | **no** | ecspresso 内部で `env`/`must_env` が読むだけ、tf state に反映されない |

### ライフサイクル

- **Create**: `ecspresso deploy`(v2では新規/更新の区別なし)。computed attribute を AWS から取得して state に書く
- **Read**: DescribeServicesでACTIVE確認、computed更新、存在しなければ `RemoveResource`。**input(`tfstate_values` / `tfstate_func_prefix` / `config_path` / `destroy_action`)を絶対に書き換えない**
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

ecspressoの設定ファイルはテンプレート記法/jsonnetで `tfstate('aws_lb_target_group.app.arn')` のような記述ができ、CLI運用ではtfstateファイルを読んでリソースIDを解決する。

しかしProvider経由で実行する場合、tfstateファイルは原理的に**常に1ステップ遅れる**:

- 初回 `terraform apply` 時にはtfstateはまだ書かれていない
- 2回目以降も、apply完了までtfstateには反映されない(同一apply中の変更値は引けない)

これはProvider実装の問題ではなく、tfstateというデータソースの性質。

### 解決: tfstate-lookup に override 機構を持たせる

「Provider経由のとき外部注入」の仕掛けは ecspresso ではなく、より下層の [tfstate-lookup](https://github.com/fujiwara/tfstate-lookup) に置く。これが最も依存層が薄く、ecspresso 側に tf-provider 特有の概念を漏らさずに済む。

tfstate-lookup 側に追加した API:

```go
state, _ := tfstate.ReadURL(ctx, "s3://...")
state.SetOverrides(map[string]any{
    "aws_iam_role.task":            map[string]any{"arn": "arn:aws:iam::...:role/task"},
    "aws_lb_target_group.app.arn":  "arn:aws:elasticloadbalancing:...",
})
// 以降の Lookup は overrides を優先し、ヒットしないキーはファイル/URL fallback
```

設計上のポイント:

- override の単位は **resource レベル**(`aws_foo.bar` 全体を渡す)でも **値レベル**(`aws_foo.bar.value`)でも受け付ける。tfstate-lookup の既存の longest-prefix-match + jq navigation がそのまま効くので、provider 側はユーザーが渡した形をそのまま渡せばよい
- ファイル/URL の値より override が**常に優先**(長さタイブレークでも override 勝ち)
- override に無いキーは既存のスキャン済 state を引く。「同じ tfconfig 内のリソースは provider が渡す、別 tfstate 由来は URL fallback」が自然に成立

### ecspresso 側はジェネリックな plugin instance 注入の口を持つだけ

ecspresso 側は「呼び出し側が用意した plugin instance を Load 前に渡せる」だけの薄い仕掛けに留める:

```go
// AppOption。pre-provided な instance を loader に渡す
func WithPluginInstance(name, funcPrefix string, instance PluginInstance) AppOption { ... }
```

provider 側は overrides を載せた `*tfstate.TFState` を構築して `New()` に渡す:

```go
state := tfstate.Empty()
state.SetOverrides(overrides)
app, _ := ecspresso.New(ctx, cliOpts,
    ecspresso.WithPluginInstance("tfstate", funcPrefix, state))
```

注入された instance は loader の 2-pass のうち pass 1(plugin Setup: tfstate ならファイル/URL 読み込み)をバイパスし、自身の関数を直接登録する。これにより pass 2 で評価される config-level フィールド(`cluster: tfstate(...)` 等)も overrides から解決できる。当初は `New()` 後に `app.PluginInstance(...).SetOverrides(...)` を呼ぶ設計だったが、それでは Load 時の config-level 解決に間に合わないため、Load 前注入の `WithPluginInstance` に変更した([kayac/ecspresso#1031](https://github.com/kayac/ecspresso/pull/1031))。

instance は `PluginInstance` interface(`FuncMap` / `JsonnetNativeFuncs`)で抽象化されているので、ecspresso の API 表面に `TFState()` のような型特化メソッドを生やさずに済む。将来 ssm/cfn 等で同じパターンが必要になっても影響ゼロで対応できる。

### ecspresso.yml は何も特別なことを書かない

以前の設計では `from_provider: true` のような注入受け入れフラグを置いていたが、**実装では廃止**。理由:

- 注入を受けるかどうかは ecspresso.yml ではなく provider 側が決めるべき(誰が呼ぶかを yml が気にする必要がない)
- 「provider 経由でも CLI 経由でも同じ yml が動く」性質はフラグ無しでも自動的に成立する(provider が呼ばないなら `SetOverrides` も呼ばれない)

```yaml
plugins:
  - name: tfstate
    config:
      url: s3://.../terraform.tfstate   # CLI 経由でも provider 経由でも同じ
```

### どの tfstate plugin に注入するかの指定

ecspresso では tfstate plugin を複数並べて `func_prefix` で区別できる(別 tfstate を別関数名で引く)。provider 側はこれに合わせて `tfstate_func_prefix` を持つ:

- 大半のケース(tfstate plugin が 1 個、`func_prefix` 無し)では指定不要。default `""` で素の `tfstate(...)` 関数に注入される
- 複数 plugin がある場合だけ `tfstate_func_prefix = "shared_"` のように対象を選ぶ。注入対象**以外**の tfstate plugin は従来どおり自身の Setup が走り、自分の url/path を読む(「別 tfstate はファイルから」が成立)
- ただし `tfstate_func_prefix` が config 上のどの tfstate plugin の `func_prefix` とも一致しないと、`tfstate_values` がどの lookup にも効かず、その lookup は無言でファイルを読む(prefix の指定ミスの典型)。これを検知して apply 時に warning を出す(`funcPrefixWarning`)。tfstate plugin が 1 個も宣言されていない「plugins ブロック省略」モードは正常系なので警告しない

#### terraform 側の使用例

```hcl
resource "ecspresso_service" "app" {
  config_path = "${path.module}/ecspresso.yml"

  tfstate_values = jsonencode({
    "aws_lb_target_group.app.arn" = aws_lb_target_group.app.arn
    "aws_iam_role.task"           = aws_iam_role.task   # resource 丸ごと渡してもよい
    "aws_ecs_cluster.main.name"   = aws_ecs_cluster.main.name
    "aws_security_group.app.id"   = aws_security_group.app.id
  })
}
```

terraform のグラフでこの参照が依存解決されるため、tfstate ファイルを介さない。「1ステップ遅れ」問題が原理的に発生しない。

### フォールバックを残す理由

「ネットワーク系は別tfstate」のような構成への配慮。Provider が渡せるのは同じ tfconfig 内のリソースだけなので、別 tfstate 由来のリソース ID(VPC・サブネット等)は引き続き `url` / `path` fallback で解決させたい。

## ecspresso 本体・tfstate-lookup 本体への変更

両方ともこの provider を機能させるために必須。以下を upstream に取り込んでもらう想定:

### tfstate-lookup

- `TFState.SetOverrides(map[string]any)` を追加
- `Lookup` の挙動を「overrides → scanned state」の優先順に変更(exact match / prefix match の両段階で overrides を先に見る)
- `Empty()` constructor を追加(URL/path 無しで TFState を作れるように)

### ecspresso

- `PluginInstance` interface(`FuncMap` / `JsonnetNativeFuncs`)と `WithPluginInstance(name, funcPrefix, instance)` AppOption を追加([kayac/ecspresso#1031](https://github.com/kayac/ecspresso/pull/1031))
- 2-pass loader で、pre-provided な instance は対応する plugin の Setup をバイパスして関数を直接登録する。config plugin に対応しない instance もそのまま登録されるので、`plugins:` ブロックごと省略できる
- ecspresso 本体が tfstate-lookup の新 API に依存するためのバージョン更新

tfstate-lookup 側(`Empty()` / `SetOverrides`)は `v1.12.x` としてリリース済み。ecspresso 側の `WithPluginInstance` は v3 に向けた `pre-v3` ブランチにあり、正式タグが切られるまでは Go pseudo-version で参照している(`go.mod replace` は不要)。

## 残る論点

### 1. valuesに書き漏らすと実行時エラー

ecspresso.yml 内のすべての tfstate 参照を網羅して `tfstate_values` に書く必要があり、漏れると apply 実行中に「key not found」で落ちる(URL/path fallback がヒットすれば古い値で通ってしまう可能性もある)。

ecspresso.yml/jsonnet をパースして不足キーを plan 段階で警告する案、`ecspresso render` を plan 中に実行して検証する案を検討したが、いずれも見送り:

- 静的解析は jsonnet を含むテンプレートの全実行パスを追う必要があり、現実的に網羅できない
- render ベース検証は、deploy トリガーとなる `tfstate_values` 自体が他リソース由来で Unknown を含むケースがほとんど(=検証したい時に限って Unknown を含む=render skip)で機能しない
- 文字列以外(object/list/bool/number)の Unknown を埋める適切なプレースホルダが存在しないため、Unknown を仮値に差し替えて render する手も成り立たない

→ apply 時の ecspresso 由来のエラーメッセージ(該当 jsonnet ファイルの行・列・スタックトレースを含む)で十分とし、plan 時検証は諦める方針で確定。

ただし **refresh(Read)経路だけは寛容にする**。Read は「最新の config ファイル」を「state にある(古いかもしれない)`tfstate_values`」でレンダリングするため、config-level `tfstate(...)` が参照するキーがまだ state に無い(同じ apply で作られる/直前に編集した)と落ちる。#1031 で S3 scanned-state fallback を消したことで、以前はそれに救われていた refresh がエラーになるようになった。そこで Read では config のロード/レンダリング失敗(`ConfigLoadError`)を hard error にせず、refresh をスキップして prior state を保持し warning を出す(AWS API エラーは従来どおり hard error)。これにより「クラスタ未作成の状態からの plan → apply 一発構築」や「新規依存を足した apply」が通る。本当に config が壊れていれば Create/Update 側で従来どおり落ちる。

### 1b. 初回構築での inconsistent final plan(whole-object 参照)

`tfstate_values` に `aws_subnet.public`(リソース丸ごと)のような値を渡し、その参照先が同じ apply で作られる場合、構造化(object/Dynamic)属性では Terraform が **per-leaf** で plan/apply の整合をチェックする。subnet には `outpost_arn`(null→`""`)・`enable_lni_at_device_index`(null→`0`)のように **plan 時 null で apply 時に具体値**になる computed 葉があり、`Provider produced inconsistent final plan` で落ちる。Optional 非 Computed では「plan 値 = config 値」を Terraform が強制するため、provider が plan 層で unknown へ逃がすこともできない(Dynamic を Computed 化 + 型付き unknown も `does not match config value` で拒否されることを確認済み)。

→ 解決策は **`tfstate_values` を Dynamic ではなく JSON 文字列(`string`)にする**こと。`jsonencode({...})` で渡すと、jsonencode は中に unknown を含むと **文字列全体が unknown** になり、plan は `(known after apply)` → apply で具体値の1文字列、という unknown→known 遷移になって整合チェックを通る(中身は不透明な1値なので per-leaf チェックが起きない)。provider 側は `tfstateOverridesFromPlan` で `json.Unmarshal` して overrides にするだけで、`attr.Value` を再帰走査する変換(`attrValueToGo` 系)は不要になり実装も縮む。**破壊的変更**(object literal は受け付けなくなる)だが、リリース済みの利用者がいない pre-1.0 のため StateUpgrader 無しで踏み切った。

### 2. 強制redeployのUX

「タスク定義は変えてないが force new deployment したい」ケース:

- 第一選択: `ecspresso deploy --force-new-deployment` を CLI で叩く(日常運用領分)
- tf-native な force-roll は意図的に持たない(`terraform apply -replace=` は destroy→create でダウンタイム発生のため、運用上の選択肢にしない)

`triggers` / `force_new_deployment` は持たない方針(Arguments セクション参照)。

### 3. ecspressoバージョンの整合性

Provider に同梱する ecspresso バージョンと、開発者ローカルの CLI バージョンが乖離するとデプロイ挙動が微妙にずれる可能性。

→ Provider バージョンと対応 ecspresso バージョンを明示する運用が必要。

### 4. 並列applyとの相性

同一クラスタの複数サービスを terraform が並列 apply すると、ecspresso 内部の AWS API 呼び出しがスロットリングに当たりやすい。

→ `-parallelism` の制御を意識する必要。

### 5. CodeDeploy controllerでのdestroy順序

`aws_codedeploy_deployment_group` との依存グラフが循環しないよう、Provider 側で「ecspresso 経由で scale 0 → service delete してから deployment group 削除」を吸収する設計を検討。あるいは `destroy_action = "ignore"` で逃がしてもらう。

### 6. import の扱い

`terraform import` は実装しない方針で確定。

理由:
- リソースの identity は `ecspresso.yml` + その配下のtask/service definition templatesにあり、cluster/service名のペアでは復元しきれない
- `terraform import` を実装したとしてもユーザーは `config_path` / `tfstate_values` / `tfstate_func_prefix` / `destroy_action` を `.tf` に書く必要があり、import独自の体験的メリットがない
- `ecspresso deploy` がidempotent(task/service definition の差分が無ければ no-op 相当)なので、既存サービスの取り込みは「リソースを `.tf` に書いて初回 `terraform apply` を打つ」だけで安全に達成できる

→ 運用上の手順は README の "Adopting an existing ECS service (no `terraform import`)" に明文化済み。

## 進め方

- [x] **Phase 0**: テンプレ scaffolding の片付け(`cmd/FIXME` 削除、`main.go` を providerserver 化、Makefile / .goreleaser.yml を Terraform Provider 配布規約に合わせる、README 実体化)
- [x] **Phase 1**: Plugin Framework での Provider 骨組み(`provider.ecspresso`、`ecspresso_service` resource のフル schema、CRUD は "not implemented" diagnostic を返すスタブ)
- [x] **Phase 2**: Create / Read / Update / Delete の実装(`ecspresso v2` を Go ライブラリとして `internal/ecspressoapi` 経由で薄くラップ。`config_path` は RequiresReplace。`destroy_action = "ignore"` は AWS に触らず state からのみ削除)
- [x] **Phase 3**: `tfstate_values` 注入機構の実装
  - tfstate-lookup に `SetOverrides` / `Empty` 追加(v1.12.0 リリース済み)
  - ecspresso に `pluginInstances` / `App.PluginInstance` 追加(v2.8.4 リリース済み)
  - provider 側で `tfstate_values` (Dynamic) と `tfstate_func_prefix` を schema に追加、Create/Update/Delete から `SetOverrides` を呼ぶ
  - sensitive marker 保持のため `resp.State.Raw = req.Plan.Raw` パターンで Computed のみ上書き
  - task_definition_* computed attribute を削除(authoritative に保てない値)
- [x] **Phase 4**: tfstate-lookup / ecspresso の変更を upstream PR 化(tfstate-lookup v1.12.0 / ecspresso v2.8.4 として released)
- [x] **Phase 5**: Provider UX 改善
  - `destroy_action` のみの変更で `ecspresso deploy` をスキップ(state-only update)
  - `last_apply_at` 属性を導入し、`ModifyPlan` で「次の apply で deploy が走るか」を `terraform plan` に可視化
- [x] **Phase 6**: ecspresso v2 に tfstate plugin の `optional: true` フラグ追加([kayac/ecspresso#1017](https://github.com/kayac/ecspresso/pull/1017) merge 済み)。初回 apply 時にまだ存在しない tfstate を許容するため(後述 Phase 10 で `optional: true` 依存自体が不要になった)
- [x] **Phase 7**: Terraform Registry への初回 publish。v0.0.3 で公開、ファイル名は `_v` を含まない Registry 規約に揃え済み
- [x] **Phase 8**: Registry documentation の自動生成(`tfplugindocs` で `docs/index.md` + `docs/resources/service.md`、`examples/` 配置、`make docs` ターゲット)
- [x] **Phase 9**: acceptance test (`TF_ACC=1`) の足場整備
  - `terraform-plugin-testing` を依存に追加、`internal/provider/{provider,service_resource}_acc_test.go` で Create/Read/Delete を一巡する最小 test、`make acc-test` ターゲット
  - `examples/acceptance/bootstrap/` に再現可能な fixture(空 ECS cluster + task execution role + SG、固定費 0、S3 backend で state を保存)
  - `examples/acceptance/oidc/` に GitHub Actions 用の OIDC provider + IAM Role を一発で用意する Terraform。Role の policy は acc-test cluster/service/taskdef + 既存 task execution role への `iam:PassRole` + bootstrap tfstate への `s3:GetObject` のみで、`Create*` を持たない最小権限
  - `.github/workflows/acc-test.yml` で workflow_dispatch から `make acc-test` を実行(`environment: acc-test` で OIDC role を assume)
- [x] **Phase 10**: 注入機構を `App.PluginInstance` 取り出し + `New()` 後の `SetOverrides` から、Load 前注入の `WithPluginInstance` AppOption に移行([kayac/ecspresso#1031](https://github.com/kayac/ecspresso/pull/1031))。これにより config-level の `tfstate(...)`(`cluster`/`service`/`region`)も `tfstate_values` から解決でき、`plugins:` ブロックと `optional: true` workaround の両方が不要になった。Provider は ecspresso の `pre-v3` ブランチ HEAD を Go pseudo-version で参照中(正式タグ付けは v3 リリースを待つ)

## TODO

- [x] tfstate-lookup の override API の upstream PR(v1.12.0 released)
- [x] ecspresso の plugin instance registry の upstream PR(v2.8.4 released)
- [x] ecspresso の tfstate plugin に `optional: true` を追加(post-v2.8.4 / v2 branch)
- [x] Terraform Registry への publish(v0.0.3 で公開済み)
- [x] Registry documentation の自動生成(`tfplugindocs`)
- [x] acceptance test (`TF_ACC=1`) のセットアップ(手元 / GitHub Actions 両方)
- [ ] CI で docs drift 検出(任意)
- [ ] ecspresso を tagged release (v2.8.5+) に差し替えて pseudo-version を撤去(provider 安定後)
- ~~tfstate 参照の静的解析(不足キー検出)~~ → 残る論点 #1 参照。`tfstate_values` の Unknown を扱う適切なプレースホルダがなく成り立たないため見送り
