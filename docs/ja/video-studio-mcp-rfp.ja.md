# RFP: video-studio-mcp

> Generated: 2026-07-05
> Status: Draft

## 1. Problem Statement

プレゼン資料を「1本のナレーション付き動画」に仕立てる **最終合成工程** を、決定的かつ再現可能にする MCP サーバー。資料作成（Claude Code / Cowork）と音声合成（voice-studio-mcp）は上流で完結している前提で、本ツールは **「ワークスペースにページ画像とページ音声がペアで並んでいる」状態だけを入力契約** とする。`master` ツールで各ページを音声長ぴったりのセグメントに合成し、全ページを連結して 1 本の MP4 を出力する。尺は各ページの音声長で自動決定されるため、動画と音声の同期問題が原理的に発生しない。スライドの元フォーマット（Marp / pptx / HTML）に一切依存しないことで、上流ツールと疎結合を保つ。

**対象ユーザー**: Claude Code / Cowork で資料と台本を作る本人（社内・個人）。プレゼン録画・製品紹介・社内共有動画・SNS 向けミュート字幕動画などを、手作業の動画編集なしで生成したい人。

**スコープ確定**: 本ツールは「画像＋音声 → 動画」の **純合成器**。スライドの画像化や音声合成は担わない（上流／ワークフロー skill の責務）。

## 2. Functional Specification

### Commands / API Surface

第一義は **MCP ツール**。CLI サブコマンドはローカル実行／テスト用の副次インターフェース（Go 単一バイナリ＋サブコマンド）。

| MCP ツール | 役割 |
|---|---|
| `get_usage` | ワークスペースモデル・マニフェストスキーマ・リカバリ表・ffmpeg/フォント前提を返す（初回必読、voice-studio-mcp と同じ作法） |
| `master` | マニフェストを読み、各ページを音声長のセグメントに合成→連結→1 本の MP4 を出力し、そのパスを返す |
| `check_job` | 長尺で非同期化した場合の進捗確認（Phase 2） |

`synthesize` 相当は持たない（音声は voice-studio-mcp の責務）。合成は `master` 一点に集中。

### Input / Output

**入力**:
- **ワークスペースディレクトリ**: エージェントが素材（ページ画像・ページ音声）を置いたディレクトリ。そこをワークスペースとして扱う。ファイルの受け渡しはエージェントのファイル操作能力に委ね、本 MCP はバイト列転送ツールを持たない（voice-studio-mcp と同一モデル）。
- **マニフェスト JSONL**: 1 行 = 1 ページ。順序・ペアリング・ページ別設定を明示的に持つ。

```jsonl
{ "image": "p01.png", "audio": "p01.wav", "caption": "…", "transition": "cut" }
{ "image": "p02.png", "audio": "p02.wav", "caption": "…", "transition": "fade" }
```

**出力**:
- `master.mp4`（H.264 / AAC / yuv420p）
- 中間セグメントは保持 / 破棄を選択可能

**既定値**: 1920×1080・16:9・ハードカット・**字幕 off**（マニフェスト or config で opt-in）。解像度／アスペクト／既定トランジションは config・マニフェストで上書き可能。

### Configuration

- config ファイル（解像度・アスペクト・既定トランジション・既定フォント・中間物保持など）
- ワークスペース単位のマニフェスト JSONL

### External Dependencies

- **ffmpeg**（合成の本体）: 子プロセス実行。voice-studio-mcp が AivisSpeech Engine を、data-toolbox-mcp が Podman/DuckDB を抱えるのと同じ扱い。ホスト非存在時は明快なエラー＋導入手順を `get_usage` に記載。
- **CJK フォント（字幕使用時のみ）**: Noto Sans JP（OFL）を同梱し既定に。`fontfile` で上書き可能。

## 3. Design Decisions

- **なぜ MCP か**: Cowork の VM サンドボックスからローカル CLI を直接キックできない公算が高い。MCP なら登録済みツールとして呼び出せる。→ 第一義は MCP、CLI サブコマンドは副次。
- **なぜ Go か**: org 標準。単一バイナリ＋サブコマンド。voice-studio-mcp / data-toolbox-mcp スケルトンを移植でき、ffmpeg の子プロセス管理も既存パターンで書ける。
- **命名（video-studio-mcp）**: `voice-studio-mcp`（音声担当）↔ `video-studio-mcp`（映像担当）の対。エコシステム上の役割分担を名前で表現でき、発見性が高い。代替案として `slidecast-studio-mcp`（意味的に最精密だが語の認知度が低い）、`presentation-studio-mcp`（用途明快だがやや狭い）を検討し、対称性を優先して video-studio-mcp を採用。
- **補完関係**: `voice-studio-mcp`（音声供給）／`multi-actor-narration` skill（上流の資料→台本→音声オーケストレーション）／pptx・Marp（資料作成）。本ツールはその **最終合成レイヤ**。
- **言語中立という利点**: voice-studio-mcp が日本語専用なのに対し、本ツールは画像＋音声を綴じるだけなので **言語非依存**。将来 voice-studio 以外の音声源・多言語プレゼンにもそのまま使える。
- **字幕とフォント**: 字幕焼き込みは日本語＝CJK のフォント解決に依存し、署名・notarize した配布バイナリはシステムフォントの存在を前提にできない。data-toolbox-mcp が matplotlib 用に CJK フォントを同梱している前例に倣い、**Noto Sans JP（OFL）を同梱＋ `fontfile` 上書き＋未解決時の明快なエラー**とする。OFL の帰属表記を行う。
- **明示的なスコープ外**: 資料作成／スライドの画像化（Marp・pptx→PNG）／音声合成／高度な動画編集（多トラック合成・BGM・凝ったアニメーション）。字幕はキャプション焼き込みまでで、モーショングラフィックスは持たない。
- **上流ワークフロー skill は本 RFP のスコープ外**: MCP 完成後、実際の使い勝手を見てから skills-series で別起票する。

## 4. Development Plan

### Phase 1: Core

- スケルトン移植（voice-studio-mcp / data-toolbox-mcp）: Go 単一バイナリ＋サブコマンド＋MCP(stdio)
- マニフェスト JSONL パース＋バリデーション（image/audio 存在確認、ページ順序、strict JSON decode）
- ffmpeg コマンド生成（画像ループ＋音声 → 音声長セグメント）＋ concat 連結 → `master.mp4`
- MCP ツール `get_usage` / `master`
- ハードカットのみ・字幕なし・既定 1920×1080
- **ページ単位チャプターマーカー**（ffmetadata; 既定 ON; title→`Page N`; concat ステップに相乗り。ADR-0002）
- **テスト**: マニフェストパース、ペアリング検証、**ffmpeg 引数生成（純関数・実行はモック）**、リカバリ経路
- → *独立レビュー可*（合成パイプラインの心臓部）

### Phase 2: Features

- **非同期レンダリング `check_job`（実装済 2026-07-06, ADR-0003）** — `master async:true` でジョブ投入 →`check_job` で進捗/結果取得。
- 字幕（`caption`）— **方式転換予定: 焼き込み＋CJKフォント同梱ではなく、クローズドキャプション（`mov_text` ソフト字幕トラック）を採用**。理由: 実環境の ffmpeg が `drawtext`/`subtitles`（libfreetype/libass）非対応で、ユーザー環境でも前提にできないと判明。ソフト字幕トラックなら core muxing のみで動作し、**CJK フォント同梱・OFL 依存が不要化**。詳細は実装時に ADR-0004 で確定。
- フェード等トランジション（xfade）、解像度／アスペクト上書き
- 中間セグメント保持／破棄オプション
- → 各機能を独立レビュー可

### Phase 3: Release

- README.md / README.ja.md / CHANGELOG.md / AGENTS.md、`get_usage` ドキュメント完全性
- 同梱フォントの OFL 帰属表記（`licenses`）
- マルチプラットフォームビルド、darwin は署名＋notarize
- umbrella(util-series) submodule ポインタ更新、org profile 追記、`check-org.sh`

**独立レビュー可能な区切り**: Phase 1（合成パイプライン単体）、Phase 2（字幕・トランジション等を機能ごと）。

## 5. Required API Scopes / Permissions

**None** — 外部サービス・認証情報なし。ffmpeg はローカル子プロセスのみ。

## 6. Series Placement

Series: **util-series**
Reason: voice-studio-mcp / data-toolbox-mcp / ask-llm-mcp と同じく util-series の MCP サーバー群に属する。素材変換的（画像＋音声 → 動画）でパイプ的な性格も util-series と整合する。

## 7. External Platform Constraints

- **MCP クライアント登録**: Cowork / Claude Code に MCP サーバーとして登録して使う。ワークスペースのディレクトリは **MCP プロセスから到達可能な場所**（エージェントが素材を置く場所と同一ファイルシステム）である必要がある。
- **ffmpeg 前提**: ホストに ffmpeg が存在すること（子プロセス実行）。無い場合は明快なエラー＋導入手順を `get_usage` に記載。
- **コーデック互換**: 幅広い再生互換のため H.264 / AAC / **yuv420p** を既定。concat のためストリームパラメータを全セグメントで統一。
- **字幕フォント**: 字幕使用時は CJK フォント解決が必要。既定は同梱 Noto Sans JP、`fontfile` で上書き可能。

---

## Discussion Log

- **着想**: Claude Code / Cowork で資料＋ページ別トークスクリプトを作り、voice-studio-mcp でページ音声を生成、各ページ画像＋音声をセットに 1 本のプレゼンビデオを自動生成する構想から出発。
- **スコープ（案A 確定）**: 本ツールは「画像＋音声 → 動画」の純合成器に限定。スライド画像化・音声合成は上流／ワークフロー skill の責務とし、責務最小化とテスト容易性を優先（voice-studio-mcp が音声合成に専念しているのと同じ切り方）。
- **なぜ MCP か**: Cowork の VM サンドボックスからローカル CLI をキックできない公算が高いため、MCP を第一義のインターフェースに採用。
- **ペアリング（案A 確定）**: ファイル名規約でなく **マニフェスト JSONL**。voice-studio の script JSONL と対称的で、字幕・トランジションをページ単位で保持でき、順序も明示的。
- **字幕（既定 off 確定）**: 標準搭載しつつ既定は off の opt-in。
- **ワークスペース／ファイル受け渡し**: エージェントが素材を置いたディレクトリをワークスペースとして扱い、ファイル受け渡しはエージェントのファイル操作能力に委ねる（voice-studio-mcp と同一モデル）。バイト列転送ツールは持たない。
- **命名**: `voice-studio-mcp` との対称性を優先し **video-studio-mcp** を採用。`slidecast-studio-mcp` / `presentation-studio-mcp` も検討。
- **字幕フォント問題（案A 確定）**: CJK フォント解決リスクを踏まえ、Noto Sans JP（OFL）を同梱＋ `fontfile` 上書き＋未解決時エラー。data-toolbox-mcp の CJK フォント同梱前例に倣う。OFL 帰属表記を行い、字幕とともに Phase 2 スコープへ。
- **上流ワークフロー skill**: 本 RFP のスコープ外。MCP 完成後に skills-series で別起票。
