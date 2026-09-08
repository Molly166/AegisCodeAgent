# AegisCodeAgent

[English](README.md) | [简体中文](README.zh-CN.md) | [日本語](README.ja.md) | [Español](README.es.md)

GitHub の PR 上で動作する、Go 向けコードレビュー Agent です。静的解析、リポジトリのコンテキスト、モデル推論、独立した証拠検証を組み合わせ、P0–P3 の指摘と HTML レポートを返します。常駐サーバーは不要です。

> **v1 リリース候補の開発版。** 再利用可能な Workflow、複数 Provider、隔離実行、実コード評価を実装しています。リリースタグの公開、実 API の品質評価、レポートサイトのデプロイが完了したという意味ではありません。[検証状況](docs/validation.md)。

## 動作とアーキテクチャ

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

`.github/workflows/aegis.yml` を追加します。Aegis のソースをコピーする必要はありません。

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

OrcaRouter は `provider: orcarouter` と専用 `ORCAROUTER_API_KEY` を使用します。モデル ID は利用前に確認してください。API を利用しない場合は `provider: none` とし secrets を省略します。汎用 OpenAI-compatible API の HTTPS endpoint とモデル能力は明示的に設定します。[Provider 設定](docs/providers.md)。

Fork/Dependabot PR にモデルキーは渡りません。コメント権限がなくても Check と Artifact にレポートが残ります。Ruleset で実際に表示される **Aegis merge gate** を必須チェックに設定しなければ、GitHub のマージ制限にはなりません。最初の旧 Base からの移行は fail closed となることがあり、メンテナーの明示的な移行レビューが必要です。[詳細な導入手順](docs/github-action.md)。

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

- [公開 HTTPS レポート](docs/report-hosting.md): 既定は Artifact。Pages 公開は別途手動承認し、機密コードを公開しないでください。
- [リリース候補のビルド](docs/github-action.md): 六つの OS/アーキテクチャ、SHA256。ビルドだけではタグや Release を公開しません。
- [貢献](CONTRIBUTING.md)・[セキュリティ](SECURITY.md)・[検証記録](docs/validation.md)

[MIT License](LICENSE)。
