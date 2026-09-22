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

> **状態:** リリース済み。ツール: `get_usage`, `master`, `check_job`。字幕は
> 焼き込み（`captions`）とクローズドキャプショントラック（`soft_captions`）の
> 両形式とも実装済みで、`transition: "fade"` によるページ間ディップも実装済み。
> 本当のページ間ディゾルブはスコープ外（尺厳密性の保証を壊すため。
> [ADR-0007](docs/ja/adr/0007-fade-transitions.ja.md) 参照）。現行バージョンは
> [CHANGELOG.md](CHANGELOG.md) を参照。

## なぜ CLI でなく MCP か

想定クライアントは Claude Code / Cowork。Cowork の VM サンドボックスは
ローカル CLI を直接起動できない公算が高いが、登録済みの MCP ツールなら呼べる ——
だから MCP インターフェースが本体である。`serve` サブコマンドが入口で、CLI 側
（`doctor`, `version`）はローカル診断用。バージョンは `--version` でも取得でき、
`version` サブコマンドと同一の文字列を出力する。

## 必要環境

- `PATH` に **ffmpeg** と **ffprobe**（`brew install ffmpeg`）。ffprobe は
  ffmpeg に同梱され、各ページの音声長の測定に使う。
- ビルドに Go 1.25+。

## インストール / ビルド

```sh
make build      # → dist/video-studio-mcp（darwin は自動 codesign）
make test       # go test ./...（ffmpeg 不要のハーメティックテスト）
make package    # 4プラットフォームをクロスビルド（darwin は arm64 のみ）+ zip/tar.gz + darwin notarize
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

1ワークスペース = 1デッキ: `<work_dir>/<workspace_id>/`

```
<manifest>.jsonl   ページマニフェスト   （あなたが書く）
images/…           ページ静止画         （あなたが置く）
audio/…            ページ音声           （あなたが置く）
output/            出力mp4 + tmp        （サーバーが書く）
```

`work_dir` はあなたが用意した絶対パス（自前のファイルツールでディレクトリを
作り素材を配置）で、`master` 呼び出し時に渡す。必須で既定値は無い — サーバーが
選んだディレクトリは呼び出し側が開けるとは限らず、返したパスが役に立たなくなる
ため。マニフェスト内の素材パスはワークスペースルート相対。
サーバーはワークスペース外を読み書きしない（`os.Root` によるカーネル強制。外部を
指すシンボリックリンクは `path_not_allowed` で拒否）。ワークスペースディレクトリ
自体も I/O 前に検査する — `<work_dir>/<id>` が実ディレクトリでなくシンボリック
リンクだった場合、リンク先に対してレンダリング全体を実行するのではなく、id の
解決先を名指しして拒否する。

システム領域、ホームディレクトリそのもの、資格情報/エージェント制御の場所
（`~/.ssh`、`~/.aws`、`~/.claude` など）、および**このサーバー自身の設定
ディレクトリ（`~/.config/video-studio-mcp`）**を `work_dir` に指定した呼び出しは、
サブディレクトリを含め、どんな綴りで渡しても `work_dir_denied` で拒否する（判定は
[nlink-jp/pathguard](https://github.com/nlink-jp/pathguard) が行う）。work_dir は呼び出し側の
ものであり、サーバー自身のディレクトリはワークスペースではない。

`manifest_path` や、マニフェストが名指す画像・音声が、`.env`、このサーバーの設定ディレクトリ内、
資格情報ディレクトリ直下のリンクの行き先のいずれかなら、読む前に `path_not_allowed` で拒否する。
ファイルがあってもなくても同じ答え。ワークスペースはそうした場所を含み得る（`~/.ssh/config` が
リンクする同期フォルダ内など）。各ページは ffmpeg が開く直前にもう一度検査し、パスに `%` を含む
ページ（名前でも `work_dir` でも）は拒む（ffmpeg がほかのファイルを指すパターンとして読むため）。別の
Unicode 正規化の名前とハードリンクはまだ通る。限界は [ADR-0009](docs/ja/adr/0009-pathguard.ja.md) に挙げる。

## ツール

| ツール | 目的 |
|------|---------|
| `get_usage` | 操作マニュアル（ワークスペースモデル・マニフェストスキーマ・リカバリ表）を返す。レンダリング前に一度呼ぶ。 |
| `master` | ページマニフェストから MP4 を1本生成。引数: `workspace_id`, `manifest_path`, 任意 `work_dir`, `output_name`, `chapters`（既定 true）, `captions` / `soft_captions`（既定 false）, `width`/`height`/`fps`（キャンバス上書き）, `fade_seconds`（既定 0.5）, `keep_intermediates`（既定 false）, `async`（既定 false）。 |
| `check_job` | 非同期レンダの進捗を取得: `state`・ページ進捗、`done` 時は `master` の同期結果と同じペイロード。 |

### 出力サイズ / アスペクト

キャンバスは既定でサーバー `[video]` 設定（1920×1080）。レンダごとに
`width`/`height`/`fps`（偶数・16以上）で上書きし、同じデッキを **16:9**
（1920×1080）・**9:16** 縦（1080×1920）・**1:1**（1080×1080）で出力できる ——
SNS 配信に有用。画像は常にレター/ピラーボックスで収める。ページ中間物はワークスペースの
外、ユーザーのキャッシュディレクトリ（macOS では `~/Library/Caches/video-studio-mcp`）の下の
専用ディレクトリで作って破棄する（`keep_intermediates: true` で `output/tmp` へ写す。描画が
失敗したときも写す）。ワークスペースに置くのは完成した MP4 だけ。ffmpeg がワークスペースから
読むのはページの画像と音声だけで、それぞれ自分の形式として開く。

### 字幕

マニフェストの `caption` は2通りで表示できる（独立・どちらも既定 off）:

- **焼き込み**（`captions: true`）— 常時表示のピクセル字幕。ミュートの SNS
  自動再生でも見える。Go で同梱日本語フォント（M PLUS 1p）を描画し ffmpeg の
  core `overlay` で合成するため、`drawtext`/libfreetype 無しの ffmpeg でも動作。
  スタイル（フォントサイズ・色・ボックス・余白）はサーバーの `[caption]` 設定。
- **クローズドキャプション**（`soft_captions: true`）— プレイヤーで ON/OFF できる
  `mov_text` ソフト字幕トラック。プレイヤー描画・フォント不要。ミュート自動再生
  では既定非表示だが、必要時に選択・アクセシブル。

両方有効化すれば、焼き込みピクセル＋選択可能トラックの両立も可能。

### トランジション

`transition` が `"fade"` のページは末尾でフェードアウトし、次のページは先頭で
フェードインする（ディップ先はキャンバスの `background` 色）。どちらのフェードも
**各ページ自身の尺の内側**で起きる —— ページは重ならない —— ので、総尺は音声尺の
総和のままで、チャプター/字幕のタイミングにも影響しない。裏返しとして、画像が
暗くなる間もナレーションは鳴り続けるので、気になる場合は音声末尾に少し無音を
持たせるとよい。

長さは `fade_seconds`（呼び出し単位、または `[video] fade_seconds`。既定 `0.5`）。
**どのページも必ず半分以上は全輝度**になるようクランプされ（0.8 秒ページでの
0.5 秒フェードは両側 0.2 秒になる）、1フレーム未満になるフェードは落とす。
`fade_seconds: 0` で全境界がカットに戻る。最終ページの `transition` は無視される
（次のページが無い）。`master` は `fades_applied` を報告する。

音声はフェードしない。またページ間ディゾルブは無い —— `xfade` はページを
オーバーラップさせ、尺厳密性の保証を壊すため
（[ADR-0007](docs/ja/adr/0007-fade-transitions.ja.md)）。

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

- `image`（必須）— ワークスペース相対の静止画（PNG/JPG）。描画の前にデコードし、PNG か JPEG として
  読めないもの（名前にかかわらず GIF や途中で切れたファイル）と 8192×8192 画素より大きいものは `invalid_manifest` で拒む。
- `audio`（必須）— ワークスペース相対のナレーション音声。その長さがページの表示時間。WAV・MP3・M4A/MP4/MOV・FLAC・
  Ogg/Opus・AAC・WebM/MKA・AIFF・CAF・W64・AU・AC3・WMA のどれか。中身がプレイリストや連結リストのファイルは拒否する。
- `title`（任意）— そのページの**チャプターマーカー**名（既定 `Page N`）。
- `caption`（任意）— `master` を `captions: true` で呼んだとき動画に**焼き込む**字幕テキスト（off 時は無視）。
- `transition`（任意）— **次のページ**への繋ぎ方: `cut`（既定）または `fade`。
  最終ページでは無視（繋ぐ次ページが無い）。

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

## フォント

本ツールは字幕焼き込み用に **M PLUS 1p** フォント
（[internal/caption/fonts/MPLUS1p-Regular.ttf](internal/caption/fonts/MPLUS1p-Regular.ttf)）
を同梱している。SIL Open Font License, Version 1.1 のもとでライセンスされている。
素晴らしいフォントを提供してくださった M+ FONTS Project に感謝します。
[FONTS_LICENSE](FONTS_LICENSE) 参照。

## ライセンス

MIT — [LICENSE](LICENSE) 参照。同梱の M PLUS 1p フォントは SIL Open Font
License 1.1（[フォント](#フォント)・[FONTS_LICENSE](FONTS_LICENSE) 参照）。
