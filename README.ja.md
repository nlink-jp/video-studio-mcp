# video-studio-mcp

ページ画像とページ音声から **ナレーション付きプレゼン動画**（MP4）を組み立てる
MCP stdio サーバー。マニフェストの1行が1ページで、静止画とその音声を対にし、
順に連結する。各ページは音声の長さちょうど表示されるため、動画と音声の同期は
原理的にずれない。

`video-studio-mcp` は **純合成器** である。スライド作成や音声合成は行わない ——
エージェントが各ページを自前のツールで画像化し、音声は上流（例:
[voice-studio-mcp](https://github.com/nlink-jp/voice-studio-mcp)）で用意して
ワークスペースに配置し、本サーバーがそれらを ffmpeg で mux・連結する。

```
voice-studio-mcp  ─▶  ページ音声 ─┐
                                  ├─▶  video-studio-mcp (master) ─▶  deck.mp4
スライド ─▶ ページ画像 ───────────┘
```

> **状態:** Phase 1 スキャフォールド。ツール: `get_usage`, `master`。字幕・
> トランジションは Phase 2（RFP 参照）。

## なぜ CLI でなく MCP か

想定クライアントは Claude Code / Cowork。Cowork の VM サンドボックスは
ローカル CLI を直接起動できない公算が高いが、登録済みの MCP ツールなら呼べる ——
だから MCP インターフェースが本体である。`serve` サブコマンドが入口で、CLI 側
（`doctor`, `version`）はローカル診断用。

## 必要環境

- `PATH` に **ffmpeg** と **ffprobe**（`brew install ffmpeg`）。ffprobe は
  ffmpeg に同梱され、各ページの音声長の測定に使う。
- ビルドに Go 1.25+。

## インストール / ビルド

```sh
make build      # → dist/video-studio-mcp（darwin は自動 codesign）
make test       # go test ./...（ffmpeg 不要のハーメティックテスト）
make package    # 5プラットフォームをクロスビルド + zip + darwin notarize
```

環境チェック:

```sh
dist/video-studio-mcp doctor
```

## MCP クライアントへの登録

```json
{
  "mcpServers": {
    "video-studio": {
      "command": "/absolute/path/to/video-studio-mcp",
      "args": ["serve"]
    }
  }
}
```

ワークスペースディレクトリはサーバープロセスから到達可能な場所（エージェントが
画像・音声・マニフェストを置く場所と同一ファイルシステム）である必要がある。

## ワークスペースモデル

1ワークスペース = 1デッキ: `<workspace_root>/<workspace_id>/`

```
<manifest>.jsonl   ページマニフェスト   （あなたが書く）
images/…           ページ静止画         （あなたが置く）
audio/…            ページ音声           （あなたが置く）
output/            出力mp4 + tmp        （サーバーが書く）
```

`workspace_root` はあなたが用意した絶対パス（自前のファイルツールでディレクトリを
作り素材を配置）で、`master` 呼び出し時に渡す。省略時はサーバー既定
（`~/.video-studio`）。マニフェスト内の素材パスはワークスペースルート相対。
サーバーはワークスペース外を読み書きしない（`os.Root` によるカーネル強制。外部を
指すシンボリックリンクは `path_not_allowed` で拒否）。

## ツール

| ツール | 目的 |
|------|---------|
| `get_usage` | 操作マニュアル（ワークスペースモデル・マニフェストスキーマ・リカバリ表）を返す。レンダリング前に一度呼ぶ。 |
| `master` | ページマニフェストから MP4 を1本生成。引数: `workspace_id`, `manifest_path`, 任意 `workspace_root`, `output_name`, `chapters`（既定 true）, `async`（既定 false）。 |
| `check_job` | 非同期レンダの進捗を取得: `state`・ページ進捗、`done` 時は `master` の同期結果と同じペイロード。 |

### 非同期レンダリング

長尺デッキでは `master` に `async: true` を渡す。即 `job_id` を返して
バックグラウンドで描画する。その `job_id` で `check_job` をポーリングして
`state`（`running`/`done`/`failed`）とページ進捗を取得し、`done` になれば
ステータスに完全な結果が入る。ジョブはインメモリで再起動を跨がない（未知の
`job_id` は `job_not_found` → `master` を再実行するだけ）。

## ページマニフェスト（JSONL・1行1ページ）

```jsonl
{"image":"images/p01.png","audio":"audio/p01.wav","title":"はじめに"}
{"image":"images/p02.png","audio":"audio/p02.wav","caption":"見出し","transition":"cut"}
```

- `image`（必須）— ワークスペース相対の静止画（PNG/JPG）。
- `audio`（必須）— ワークスペース相対のナレーション音声。その長さがページの表示時間。
- `title`（任意）— そのページの**チャプターマーカー**名（既定 `Page N`）。
- `caption`（任意）— **Phase 2**: 字幕焼き込み。受理するが未描画。
- `transition`（任意）— `cut`（既定）。`fade` は受理するが Phase 1 ではハードカット。

空行と `#` 始まりのコメント行は無視。

## 出力

`output/<name>.mp4` — H.264 / AAC / yuv420p、既定 **1920×1080 @ 30fps**
（`[video]` で変更可）。各ページはキャンバスに収まるようスケール+パッド
（レター/ピラーボックス）。総尺はページ音声長の合計。既定値は幅広い再生互換
（QuickTime・ブラウザ・SNS）を狙う。

既定で MP4 に**ページ単位のチャプターマーカー**を埋め込む（タイトルはマニフェスト
`title`、未指定なら `Page N`）。チャプター対応プレイヤーでスライド単位に頭出し
できる。`chapters: false` で無効化。

## 設定

[`config.example.toml`](config.example.toml) を
`~/.config/video-studio-mcp/config.toml` にコピー。全キー任意で、記載値が既定。
キャンバスサイズ・fps・x264 の `crf`/`preset`・音声ビットレート・パッドの
`background` 色を設定可能。

## ドキュメント

- [アーキテクチャ](docs/ja/reference/architecture.ja.md) — モジュール構成・
  レンダリングパイプライン・エラーモデル・テスト戦略。
- [ADR-0001: 2フェーズレンダリングパイプライン](docs/ja/adr/0001-render-pipeline.ja.md)。
- [RFP](docs/ja/video-studio-mcp-rfp.ja.md) — 承認済みスコープ。設計判断の正典。

## ライセンス

リポジトリのライセンス参照。同梱フォント（Phase 2・字幕焼き込み用）は各自の
OFL 帰属表記を伴う。
