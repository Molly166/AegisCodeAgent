# AegisCodeAgent

[![CI](https://github.com/Molly166/AegisCodeAgent/actions/workflows/ci.yml/badge.svg?branch=master)](https://github.com/Molly166/AegisCodeAgent/actions/workflows/ci.yml)
[![Aegis Code Review](https://github.com/Molly166/AegisCodeAgent/actions/workflows/aegis-review.yml/badge.svg?branch=master)](https://github.com/Molly166/AegisCodeAgent/actions/workflows/aegis-review.yml)
![Go 1.23+](https://img.shields.io/badge/Go-1.23%2B-00ADD8?logo=go&logoColor=white)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

[English](README.md) | [简体中文](README.zh-CN.md) | [日本語](README.ja.md) | [Español](README.es.md)

AegisCodeAgent は、GitHub の Pull Request 上で自動実行される Go ネイティブのコードレビュー Agent です。決定論的解析、リポジトリレベルのコンテキスト、LLM による推論、独立した検証を組み合わせ、根拠のある指摘だけを最終レビューに反映します。

> **現在のマイルストーン：v0.6。** Aegis は、リポジトリネイティブな GitHub Actions Reviewer とローカル CLI として利用できます。現在は Go を主な対象とし、最初の推論 Provider として DeepSeek を採用しています。次の段階では、定量評価と本番運用に向けた堅牢化を進めます。

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
          │                 go test · go vet
          │
          └──────────────► リポジトリコンテキストエンジン
                            AST · 型 · 呼び出し元 · テスト
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

モデルがマージ可否を直接決定することはありません。静的解析の指摘には、あらかじめ再現可能な根拠が含まれます。モデルが生成した候補は、位置、Diff、ソーススナップショット、対象を絞った診断の検証を通過した場合にのみ、最終 Finding に昇格します。却下された仮説や結論を出せなかった仮説は監査記録に残りますが、最終結果には含まれません。

## アーキテクチャ

システムは、入出力が明確なステージに分割されています。

| ステージ | 責務 | 出力 | 実装 |
| --- | --- | --- | --- |
| GitHub オーケストレーション | PR イベントへの応答、信頼済み Base と正確な Head の Checkout、古い Run のキャンセル | 再現可能なレビュー用 Worktree | `.github/workflows/aegis-review.yml` |
| Diff エンジン | Revision の安全な解決、Three-dot Diff、Rename、Binary、Hunk、変更行の解析 | 正規化済み Change Set | `internal/gitdiff/` |
| 静的解析 | Analyzer の並行実行と Timeout、診断の正規化と重複排除 | 根拠付き Findings | `internal/analyzer/` |
| コンテキストエンジン | Go の宣言と型関係を Index 化し、予算内で変更・関連 Symbol を順位付け | Repository Context Bundle | `internal/context/` |
| Reasoning Agent | DeepSeek が読み取り専用 Tool で制限された根拠を確認し、構造化された候補を提案 | 未検証 Candidates | `internal/agent/` |
| Verifier | Candidate の識別子と位置を検証し、対象を絞った Check を再実行して独立した根拠と照合 | Verified／Rejected／Inconclusive の判定 | `internal/verifier/` |
| Publisher | Severity を P0-P3 に変換し、マージ閾値を適用して GitHub 出力と完全なレポートを生成 | Summary、Annotation、HTML/JSON | `internal/githubreport/`、`internal/report/` |
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
対象を絞った Test/Vet の根拠と照合
      │
      ├── Verified ───────────────────────────────────► 最終 Finding
      ├── Rejected ───────────────────────────────────► 監査記録のみ
      └── Inconclusive ───────────────────────────────► 監査記録のみ
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
go test + go vet + repository context + GitHub report
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

ドキュメントのみを変更する Pull Request でも Workflow は起動し、Summary とレポート Artifact が生成されます。比較対象にサポート対象のソースコード変更が含まれない場合、Aegis は DeepSeek の推論と Verifier をスキップします。これにより、決定論的 Check を維持したまま不要なモデル呼び出しを避けます。

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

# 利用可能なすべての Option を確認
./aegis review --help
./aegis github --help
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
- 独立した Candidate 検証。
- 自己完結型 HTML エビデンスレポート。
- GitHub Actions の Trigger、Annotation、Artifact、Merge Gate。

今後：

- Bug/Clean PR の評価 Corpus の整備。
- Precision、Recall、False Positive、Latency、Cost の測定。
- 再利用可能な GitHub Action の Package 化と Release 配布。
- 追加の Model Provider と本番向け Observability。

## ライセンス

[MIT](LICENSE)
