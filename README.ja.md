<p align="center">
  <img src="docs/assets/aegis-pr-gate-harmony.png" alt="AegisCodeAgent" width="96" height="96" />
</p>

<h1 align="center">AegisCodeAgent</h1>

<p align="center">
  <a href="README.md">English</a> | <a href="README.zh-CN.md">简体中文</a> | <a href="README.ja.md">日本語</a> | <a href="README.es.md">Español</a>
</p>

<p align="center">
  <a href="https://www.orcarouter.ai/ref/ref_7d9895701ff01fc94d85"><img src="https://www.orcarouter.ai/orca-logo-classic.png" alt="OrcaRouter" height="32" /> OrcaRouter</a> · <a href="docs/providers.md#referral-disclosure">任意の Provider · 紹介リンクについて</a>
</p>

GitHub の PR 上で動作する、Go 向けコードレビュー Agent です。静的解析、リポジトリのコンテキスト、モデル推論、独立した証拠検証を組み合わせ、P0–P3 の指摘と HTML レポートを返します。常駐サーバーは不要です。

> **v1 リリース候補の開発版。** 再利用可能な Workflow、複数 Provider、隔離実行、実コード評価を実装しています。リリースタグの公開、実 API の品質評価、レポートサイトのデプロイが完了したという意味ではありません。[検証状況](docs/validation.md)。

> **このリビジョンでは V1 設定を選択しています。** 実装を信頼できる `master` にマージした後、四 Job・Docker 隔離・再利用 Workflow を `.github/workflows/aegis-review.yml` に設定し、一時コピーを削除しました。このローカルでの有効化は **GitHub での実行成功を意味しません**。オンライン PR、Docker サンドボックス、外部リポジトリの受け入れ検証は、このリビジョンの push とレビュー後に行う必要があります。本番認証や正式リリースではありません。[V1 受け入れチェックリスト](docs/v1-acceptance.md)と[信頼境界を守る移行手順](docs/github-action.md)に従い、対象 Head を自動的に信頼して回避しないでください。

## 動作とアーキテクチャ

以下は選択済みの v1 Workflow の設計です。オンラインでの動作は、上記の受け入れチェックリストによる検証が必要です。

```text
PR 作成 / 更新 → 信頼できるレビュー実装と正確な Base/Head を解決
    → Diff → 並列静的解析 → AST・型・呼び出し・テスト・PR 意図
    → Reasoning Agent + 制限付き読み取り専用ツール
    → Verifier: Verified / Needs Review / Rejected
    → 独立 Publisher: HTML・行アノテーション・同一 Bot コメント更新
    → Aegis merge gate
```

静的解析は `go test`、`go vet`、`staticcheck`、`gosec` を使用します。モデルの仮説は、そのまま最終指摘になりません。位置、変更行、ソーススナップショット、独立診断や限定的な意味規則で検証します。確認できない問題は `Needs Review` として残り、「問題なし」とは表示しません。

既定では証拠のある P0/P1 と未確認 P0 がブロック対象です。P2/P3 自体はブロックしません。モデルやコンテキストの任意段階の縮退と、必須証拠の不足を区別します。`require-agent: true` でモデル完了を必須にできます。

自分自身の PR は信頼できる Base からレビュー実装をビルドし、他リポジトリからの利用では呼び出された Aegis Workflow の解決済み SHA を使用します。対象 Head からビルドしません。対象 Go コードを実行する子プロセスは無ネットワーク・読み取り専用ソースの Docker 内で動作し、モデルキーは渡しません。Publisher は別 runner で JSON を検証して HTML とポリシーを再生成します。[安全境界](SECURITY.md)。

## 他のリポジトリへの導入

監査済みの V1 リビジョンが[外部リポジトリの受け入れ検証](docs/v1-acceptance.md)を通過した後、`.github/workflows/aegis.yml` を追加します。Aegis のソースをコピーする必要はありません。このリビジョンはローカルで `workflow_call` 設定を選択していますが、外部導入の検証は未完了です。旧 Workflow のみを含む過去のリビジョンは導入先として使用できません。

```yaml
name: Aegis review
on:
  pull_request:
    types: [opened, synchronize, reopened, ready_for_review]
permissions:
  actions: read
  contents: read
  pull-requests: write
jobs:
  review:
    uses: Molly166/AegisCodeAgent/.github/workflows/aegis-review.yml@REPLACE_WITH_RELEASE_COMMIT_SHA
    with:
      provider: deepseek
      model: deepseek-v4-flash
      fail-on: p1
      fail-on-needs-review: p0
    secrets:
      provider-api-key: ${{ secrets.DEEPSEEK_API_KEY }}
```

SHA のプレースホルダーを、この Workflow を含む**監査・公開済みコミットの完全 SHA**に置き換えてください。`v1` タグが既にあるとは仮定しません。Key は GitHub Actions Secrets に登録します。

OrcaRouter の設定と連携状況は以下の節を参照してください。外部モデル API を利用しない場合は `provider: none` とし secrets を省略します。汎用 OpenAI-compatible API の HTTPS endpoint とモデル能力は明示的に設定します。[Provider 設定](docs/providers.md)。

Fork/Dependabot PR にモデルキーは渡りません。コメント権限がなくても Check と Artifact にレポートが残ります。Ruleset で実際に表示される **Aegis merge gate** を必須チェックに設定しなければ、GitHub のマージ制限にはなりません。最初の旧 Base からの移行は fail closed となることがあり、メンテナーの明示的な移行レビューが必要です。[詳細な導入手順](docs/github-action.md)。

## 任意の Provider: OrcaRouter

Aegis は OrcaRouter の互換 API 経由でモデルを利用する任意のアダプターを備えています。DeepSeek への直接接続、明示的に設定した他の Provider、モデルを使わない決定的な解析モードも引き続き選択できます。

1. [紹介リンク](https://www.orcarouter.ai/ref/ref_7d9895701ff01fc94d85)または[通常の公式サイト](https://www.orcarouter.ai/)から登録し、自分の API キーを作成します。
2. `ORCAROUTER_API_KEY` を環境変数または GitHub Actions Secrets に保存します。ソースコードや JSON には保存しないでください。
3. CLI では `--agent-provider orcarouter` を選択します。Endpoint は `https://api.orcarouter.ai/v1` です。モデルのプリセット `deepseek/deepseek-v4-flash` は変更可能で、利用可能性と能力は利用前に確認してください。[コマンドと設定](docs/providers.md#orcarouter)。

> **連携状況:** アダプターとモックによる契約テストは実装済みですが、OrcaRouter の実 API による受け入れ検証は未完了です。V1 の自己レビュー設定は引き続き DeepSeek を選択します。監査済みの Workflow リビジョンが[受け入れ検証](docs/v1-acceptance.md)を通過した後、再利用 Workflow の呼び出し元で `provider: orcarouter` を明示的に選択できます。Secret を追加するだけでは自己レビューを OrcaRouter に切り替えません。

**紹介リンクの開示:** 紹介リンクに紐づくワークスペースで対象となる有料利用が発生した場合、プログラム規約に基づき、メンテナーがその利用額の 5% を紹介報酬として受け取ることがあります。利用は任意で、Provider の選択は制限されません。ロゴは任意の Provider 連携を示すものであり、ディレクトリ掲載の承認や推奨を意味しません。[詳細](docs/providers.md#referral-disclosure)。

## ローカル実行と評価

Go 1.24+ と Git が必要です。v1 は `os.Root` を使用してファイル読み取りをリポジトリ内に制限します。レビュー実装と解析側の Go ツールチェーンを揃えてください。ローカルの既定 `--sandbox host` は信頼できるコード専用です。

```sh
go test ./...
go build -trimpath -o /tmp/aegis ./cmd/aegis
/tmp/aegis review --repo . --base origin/master --head HEAD \
  --agent-provider none --analyzers default --output /tmp/aegis-review.html
/tmp/aegis eval --corpus eval/cases --output /tmp/aegis-golden.html
/tmp/aegis eval-live --corpus eval/live --agent-provider none \
  --analyzers default --output /tmp/aegis-live.html
```

`default` は go test/go vet、`all` は追加の staticcheck/gosec も要求します。CI イメージには全解析器が含まれます。作業ツリーは対象 Head と一致するクリーンな状態にしてください。設定は `--config` で明示的に読み込み、キーを JSON に保存しないでください。

評価は二種類あります。

- **50 Golden 回放ケース:** 既存レポートの照合・門禁回帰。ライブモデルの精度ではありません。
- **12 実コードケース:** 8 Bug、4 Clean の Git fixture で実際のバイナリを実行。漏検、誤ブロック、不完全、遅延、使用量と出所を保存します。

少数の合成例や語句ベースの照合は本番品質の証明ではありません。実 API 比較は明示的なモデル指定、同じ条件と複数回実行で行い、費用が発生します。Verifier の消融比較を含め、[評価ガイド](eval/README.md)を参照してください。

## 制限と関連資料

現在は Go 単一モジュールを中心とし、全言語の同等解析、汎用意味証明、自動修正、私有依存の供給を保証しません。Docker は VM 相当の完全隔離ではありません。

### HTML をダウンロードせずに閲覧する

公開ホスティングは**既定で無効**です。コードが存在しても、サイトのデプロイ完了を意味しません。公開機能を既定ブランチへマージし、Pages の Source を **GitHub Actions** に設定して、リポジトリ変数 `AEGIS_PUBLIC_REPORTS=true` を追加すると自動公開を有効にできます。`github-pages` Environment に承認者を設定した場合は各デプロイで承認を待ちます。公開機能は Review Workflow を有効化・置換せず、v1 Review の事前有効化も不要です。

旧版・v1 ともに、レポート生成 Workflow は監査済みの固定 SHA256 と一致する必要があり、v1 は publication manifest も検証します。現在の公開機能は直接実行する PR Workflow のみを対象とし、未承認の reusable／入れ子の呼び出しには対応しません。[対応する生成元と設定](docs/report-hosting.md)。

レビュー完了後にデプロイが成功すると、同じ PR Bot コメントの先頭に Web レポートのリンクを追加します。各レポートは `reports/pr-<number>/<full-head-sha>/<run-id>-<attempt>/index.html` の固有パスを持ち、検証済み JSON を `aegis-report-history` ブランチへ追記して、毎回履歴全体を再生成します。上限は 200 件・合計 64 MiB・1 件 17 MiB で、超過時は履歴を自動削除せず失敗します。公開の失敗は元の Artifact/Checks とコメントを残し、マージ判定を変更しません。

**公開リポジトリ専用です。** コード、脆弱性の詳細、履歴 JSON が公開され、リポジトリの Pages サイト全体を使用します。既存の文書サイトを上書きする設定は避けてください。変数を無効化しても公開済みデータは消えません。自動公開を有効にせず、手動実行の `confirm-public` で 1 回の公開を承認することもできます。HTTPS URL はデプロイ成功後に返されたリンクを使用してください。[設定と安全上の注意](docs/report-hosting.md)。

- [公開 HTTPS レポート](docs/report-hosting.md): 明示的な有効化後の自動履歴公開と手動再公開。既定は Artifact で、機密コードは公開しないでください。
- [リリース候補のビルド](docs/github-action.md): 六つの OS/アーキテクチャ、SHA256。ビルドだけではタグや Release を公開しません。
- [貢献](CONTRIBUTING.md)・[セキュリティ](SECURITY.md)・[検証記録](docs/validation.md)

[MIT License](LICENSE)。
