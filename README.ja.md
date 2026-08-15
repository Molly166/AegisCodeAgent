# AegisCodeAgent

[![CI](https://github.com/Molly166/AegisCodeAgent/actions/workflows/ci.yml/badge.svg?branch=master)](https://github.com/Molly166/AegisCodeAgent/actions/workflows/ci.yml)
[![Aegis Code Review](https://github.com/Molly166/AegisCodeAgent/actions/workflows/aegis-review.yml/badge.svg?branch=master)](https://github.com/Molly166/AegisCodeAgent/actions/workflows/aegis-review.yml)
![Go 1.23+](https://img.shields.io/badge/Go-1.23%2B-00ADD8?logo=go&logoColor=white)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

[English](README.md) | [简体中文](README.zh-CN.md) | [日本語](README.ja.md) | [Español](README.es.md)

AegisCodeAgent は、GitHub の Pull Request 上で自動実行される Go ネイティブのコードレビュー Agent です。決定論的解析、リポジトリレベルのコンテキスト、LLM による推論、独立した検証を組み合わせ、根拠のある指摘だけを最終レビューに反映します。

> **現在のマイルストーン：v0.7。** Replay Eval Harness、セマンティック検証、明示的な Needs Review Gate、PR の変更意図コンテキスト、4 種類の Analyzer を実行する GitHub Pipeline を実装しました。Go を主な対象とし、最初の推論 Provider として DeepSeek を採用しています。

## Pull Request を作成すると何が起きますか？

サーバーを起動したり、ローカルプロセスを常時実行したりする必要はありません。GitHub Actions が Aegis を起動し、PR の正確な Head Commit と Base を比較してレビューし、その結果を Pull Request に公開します。

```text
Pull Request の作成または更新
          │
          ▼
   信頼済み Workflow による制御
          │
          ▼
 Git Diff と変更行スコープ
          │
          ├──────────────► 決定論的 Go Analyzer
          │                 go test · go vet · staticcheck · gosec
          │
          └──────────────► リポジトリコンテキストエンジン
                            PR 意図 · Repository Rule · AST · 型 · 呼び出し元 · テスト
                                      │
                                      ▼
                            DeepSeek Reasoning Agent
                                      │
                                      ▼
                               エビデンス Verifier
                                      │
                                      ▼
                 P0-P3 Annotation · Job Summary · HTML レポート
```

モデルがマージ可否を直接決定することはありません。Candidate は位置、Diff、ソーススナップショット、対象を絞った診断、またはセマンティックな根拠を通過した場合のみ最終 Finding に昇格します。無効な仮説は Rejected、検証も否定もできない仮説は明示的な `Needs Review` となり、P0 はデフォルトで Merge を Block します。

## アーキテクチャ

システムは、入出力が明確なステージに分割されています。

| ステージ | 責務 | 出力 | 実装 |
| --- | --- | --- | --- |
| GitHub オーケストレーション | PR イベントへの応答、信頼済み Base と正確な Head の Checkout、古い Run のキャンセル | 再現可能なレビュー用 Worktree | `.github/workflows/aegis-review.yml` |
| Diff エンジン | Revision の安全な解決、Three-dot Diff、Rename、Binary、Hunk、変更行の解析 | 正規化済み Change Set | `internal/gitdiff/` |
| 静的解析 | Analyzer の並行実行と Timeout、診断の正規化と重複排除 | 根拠付き Findings | `internal/analyzer/` |
| コンテキストエンジン | PR の Title/Body/Label/Issue と Repository Guidance を Go の宣言・型関係に統合 | Repository Context Bundle | `internal/context/` |
| Reasoning Agent | DeepSeek が読み取り専用 Tool で制限された根拠を確認し、構造化された候補を提案 | 未検証 Candidates | `internal/agent/` |
| Verifier V2 | Candidate の識別子と位置を検証し、Focused Check とソース認識型 Semantic Rule を実行 | Verified／Needs Review／Rejected の判定 | `internal/verifier/` |
| Publisher | Severity を P0-P3 に変換し、マージ閾値を適用して GitHub 出力と完全なレポートを生成 | Summary、Annotation、HTML/JSON | `internal/githubreport/`、`internal/report/` |
| Eval Harness | Version 管理された Bug/Clean Report を Replay し、Finding と Gate の品質を計測 | Precision、Recall、F1、P0/P1 Recall、Gate Accuracy、False Block Rate | `internal/eval/`、`eval/cases/` |
| Credential 境界 | リポジトリ側が制御する子プロセスから Credential 形式の環境変数を除去 | Sanitized child environment | `internal/secureenv/` |

### Finding のライフサイクル

```text
決定論的な診断 ──────────────────────────────────────► 最終 Finding

モデルの Candidate
      │
      ▼
Schema + リポジトリ境界 + 変更行の検証
      │
      ▼
Focused Analyzer + Semantic Evidence の照合
      │
      ├── Verified ───────────────────────────────────► 最終 Finding
      ├── Rejected ───────────────────────────────────► 監査記録のみ
      └── Needs Review ───────────────────────────────► 人手レビューに表示し、P0 は既定で Block
```

最終レポートには、CLI、GitHub Publisher、HTML Renderer、JSON 自動化インターフェースで共有される安定した `ReviewReport` ドメインモデルを使用します。これにより、レビューのロジックと表示形式を分離しています。

## クイックスタート：GitHub 自動レビュー

このリポジトリまたは自身の Fork で Aegis を利用する場合の推奨方法です。

### 1. リポジトリを準備する

必要に応じてリポジトリを Fork し、Clone します。

```bash
git clone https://github.com/<YOUR_GITHUB_NAME>/AegisCodeAgent.git
cd AegisCodeAgent
go test ./...
```

**Settings → Actions → General** で GitHub Actions が有効になっていることを確認してください。

### 2. レビューモードを選択する

決定論的モードでは Secret は不要です。

```text
go test + go vet + staticcheck + gosec + semantic verifier + GitHub report
```

推論と検証を含む完全なフローを有効にするには、**Settings → Secrets and variables → Actions** で Repository Secret を作成します。

```text
Name:  DEEPSEEK_API_KEY
Value: <your DeepSeek API key>
```

Fork および Dependabot の Pull Request にはこの Secret は渡されず、自動的に決定論的モードが使用されます。

### 3. 通常の Feature Branch を Push する

```bash
git switch master
git pull --ff-only origin master
git switch -c feature/my-change

# コードまたはドキュメントを編集します。
git add .
git commit -m "feat: describe the change"
git push -u origin feature/my-change
```

`feature/my-change` から `master` への Pull Request を作成します。`opened` イベントで Aegis が起動し、その後の Push ごとに `synchronize` イベントが発生して新しいレビューが始まり、古い Run はキャンセルされます。

ドキュメントのみを変更する Pull Request でも Workflow は起動し、Summary とレポート Artifact が生成されます。サポート対象のソース根拠がない場合は不要なモデル推論を省略できますが、決定論的解析、Report、Merge Gate の意味は常に監査可能です。

### 4. 結果を確認する

Pull Request を開き、次の項目を確認します。

1. **Conversation**：Aegis が継続的に更新するレビューコメントと、完全な HTML レポートへのリンク。
2. **Checks → Aegis Code Review**：実行状態と Job Summary。
3. **Annotations**：変更ファイルと行に紐づく指摘。
4. **Artifacts → aegis-review-report**：`review.html` と `review.json`。
5. 最終 Check の結果：マージ可能かどうか。

| Priority | 意味 | GitHub Annotation | デフォルトでブロック |
| --- | --- | --- | :---: |
| P0 | Critical | Error | はい |
| P1 | High | Error | はい |
| P2 | Medium | Warning | いいえ |
| P3 | Low / Info | Notice | いいえ |

Reasoning Agent またはリポジトリ Context の劣化は Review Coverage の低下として表示され、それ自体が P0/P1 になることはありません。決定論的な静的解析または Verifier が未完了の場合は引き続き fail-closed とし、未解決の P0 仮説は独立した Needs Review Threshold で制御します。

結果を強制するには、`master` の Branch Ruleset で `Aegis Code Review` を Required Status Check に設定してください。Web およびメール通知は GitHub の通知設定が担当し、Aegis 自体は別のメールサービスを実行しません。

> Aegis は現在、GitHub Marketplace Action ではなく Repository-native Workflow です。このリポジトリとその Fork ではすぐに利用できます。無関係な別リポジトリへ導入するには、現時点では Aegis のソースと Workflow の両方を取り込む必要があります。再利用可能な Action としての Package 化は今後の課題です。

## クイックスタート：ローカル CLI

必要な環境：Go 1.23 以降、Git。

### API Key を使わない決定論的レビュー

```bash
go build -o aegis ./cmd/aegis

./aegis review \
  --repo . \
  --base master \
  --head HEAD \
  --output review.html
```

ブラウザで `review.html` を開きます。Analyzer は実際のファイルシステムを読み取るため、Checkout 済みの Worktree はクリーンで、`HEAD` と一致している必要があります。

### DeepSeek + Verifier による完全レビュー

```bash
cp .aegis.example.json .aegis.json
cp .env.example .env

# 実際の Key は Git に無視される .env ファイルだけに記述します。
./aegis review \
  --config .aegis.json \
  --repo . \
  --base master \
  --head HEAD \
  --output review.html
```

`.aegis.json` には、機密情報を含まない Provider 設定、予算、Timeout を保存します。API Key は `DEEPSEEK_API_KEY` または Git に無視される `.env` からのみ読み取ります。Custom Endpoint を明示的に許可しない限り、DeepSeek の公式 Endpoint だけが使用できます。

便利なコマンド：

```bash
# 機械可読レポート
./aegis review --repo . --base master --head HEAD --format json --output review.json

# staticcheck と gosec がインストール済みの場合、すべての Adapter を実行
./aegis review --repo . --base master --head HEAD --analyzers all --output review.html

# 組み込み回帰 Corpus を Replay して自己完結型 Dashboard を生成
./aegis eval --corpus ./eval/cases --format html --output eval-report.html

# 利用可能なすべての Option を確認
./aegis review --help
./aegis github --help
./aegis eval --help
```

## セキュリティモデル

- Review Binary は信頼済みの PR Base Commit から Build し、正確な Head Commit は解析対象として別の Directory に Checkout します。
- Workflow の Repository Permission は読み取り専用で、Checkout Credential の永続化を無効にしています。
- Fork および Dependabot の PR には、`DEEPSEEK_API_KEY` も書き込み可能な Token も渡されません。
- Git、Test、Vet、Staticcheck、Gosec の子プロセスから Credential 形式の環境変数を除去します。
- Model Tool は読み取り専用で、Repository Path、行数、出力量に上限があります。
- GitHub は同一リポジトリの Branch を Secret にアクセス可能な信頼済みソースとして扱います。書き込み権限を制限し、`.github/workflows/` 配下の変更には必ずレビューを要求してください。

## 開発ガイド

Package の境界：

```text
cmd/aegis/             CLI orchestration and user-facing errors
internal/gitdiff/      revision resolution and unified-diff parsing
internal/analyzer/     analyzer adapters, scheduling, normalization
internal/context/      AST/type index, relationships, ranking, budgets
internal/config/       strict JSON config and narrow dotenv loading
internal/agent/        provider protocol, prompt, tools, reasoning loop
internal/verifier/     candidate validation and evidence adjudication
internal/eval/         replay corpus, expectation matching, quality dashboard
internal/githubreport/ P0-P3 mapping, Summary, annotations, merge gate
internal/secureenv/    child-process credential isolation
internal/review/       shared domain model
internal/report/       self-contained HTML, JSON, and Markdown renderers
```

Pull Request を作成する前に、Quality Gate を実行してください。

```bash
go fmt ./...
go vet ./...
go test -race ./...
go build ./cmd/aegis
```

新しい Finding Source には、実在する Location、Severity、Category、Source、Confidence、再現可能な Evidence が必要です。新しい Agent Candidate は、検証が完了するまで最終 Findings と分離しなければなりません。

## 現在のスコープとロードマップ

実装済み：

- 決定論的 Go Analyzer Pipeline。
- リポジトリコンテキストエンジン。
- 境界を設けた DeepSeek Reasoning Loop。
- Semantic Evidence と Needs Review を備えた Verifier V2。
- Replay Eval Harness と Bug/Clean の初期回帰 Corpus。
- PR の変更意図と Repository Guidance の Context。
- go test、go vet、staticcheck、gosec をすべて実行する Workflow。
- 自己完結型 HTML エビデンスレポート。
- GitHub Actions の Trigger、Annotation、Artifact、Merge Gate。

今後：

- 初期 Corpus を独立 Label 付きの統計的に有用な Benchmark へ拡張。
- Live Model 比較、反復 Trial、Confidence Interval の追加。
- 再利用可能な GitHub Action の Package 化と Release 配布。
- 追加の Model Provider と本番向け Observability。

## ライセンス

[MIT](LICENSE)
