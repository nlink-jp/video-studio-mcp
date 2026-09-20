# アーキテクチャ — video-studio-mcp

## 目的

**ページマニフェスト**（1ページ = 静止画 + 音声）を1本の**ナレーション付き
プレゼン動画**（MP4）にする。サーバーは純合成器で、スライド描画も音声合成も
行わない。素材はエージェントが用意し、サーバーが ffmpeg で mux・連結する。

## モジュール構成

```
main.go                     → cmd.Execute
cmd/                        cobra: serve（既定） / doctor / version
internal/
  jsonrpc/                  JSON-RPC 2.0 メッセージ型 + 標準コード
  transport/                改行区切り stdio フレーミング（1行 1 MiB）
  mcpserver/                initialize / tools/list / tools/call のルーティング；
                            リッチコンテンツ用 RawResult；インプロセス Call
  logging/                  stderr への slog（+ 任意のローテートファイル）
  toolerr/                  構造化 {code,message,details} ツールエラー
  config/                   セクション TOML・strict decode・~ 展開
  workspace/                os.Root 隔離のデッキ別ディレクトリ；エージェント用意ルート
  manifest/                 ページマニフェスト JSONL パーサ（Page）
  master/                   レンダリングパイプライン（ffmpeg/ffprobe）
  tools/                    MCP ツール: get_usage, master（+ 埋め込み usage.md）
```

## レンダリングパイプライン（`internal/master`）

`Master.Build` が全工程。検証済みの `[]manifest.Page` に対し:

1. **前提チェック** — ffmpeg と ffprobe を `exec.LookPath` → `ffmpeg_not_found`。
2. **素材の解決+検証** — 各ページで `ResolveInside`（字句）→ `VerifyRegular`
   （`os.Root` 経由の Lstat）。シンボリックリンク/非通常ファイルは
   `path_not_allowed`（即時表面化）、実在しないファイルは集約して
   `manifest_incomplete`。
3. **尺の probe** — ffprobe `format=duration` を各ページ音声に実行。合計が
   総尺。
4. **セグメント生成** — 1ページ1セグメント: 静止画をループし音声と mux、
   `-t <尺>` で切り、キャンバスに収まるようスケール+パッド、`[video]` の
   パラメータで H.264/AAC/yuv420p にエンコード。全セグメントを同一パラメータで
   符号化。
5. **連結** — concat demuxer リスト（クオート安全）を書き、各セグメントを
   spawn 直前に通常ファイルとして再検証し、ストリームコピー（`-c copy`）で連結。

引数ビルダ（`segmentArgs`, `concatArgs`, `probeArgs`, `concatList`）は純関数で
直接ユニットテストする。`Runner` がコマンド実行を抽象化し、テストは
ffmpeg/ffprobe を完全にフェイクする。

1つの filtergraph でなく2フェーズにする理由: ページごとのセグメント +
concat-copy は単純・ページ数に線形・堅牢。N 入力の単一 filtergraph は複雑化し
全体を再エンコードする。ストリームコピー連結はコーデックパラメータの均一性を
要求するが、それはまさに手順4が保証するもの。

## ワークスペース隔離

1ワークスペース = 1デッキ（`<work_dir>/<id>/`）。ルートは、すべての呼び出しが
`work_dir` で渡す**エージェント用意**の絶対パスで、サーバーは自前の既定を持たない（org ADR-021）。
このルートはエージェント書込可能なので、サーバー側の全ファイル操作は
`os.Root` を通し、ワークスペース内に仕込まれたシンボリックリンクでサーバーが
外部を読み書きすることを防ぐ。ffmpeg は `os.Root` を継承できないため、入力は
spawn 直前に Lstat で再検証する。残る検証〜spawn 間の競合はローカル単一ユーザーの
脅威モデル下で受容する。

## エラーモデル

ツールエラーは `toolerr.Error{code, message, details}`。`errors.Is` は `code`
で一致するので、メッセージに関わらずセンチネルが機能する。コード:
`invalid_arguments`, `missing_argument`, `invalid_workspace_id`,
`path_not_allowed`, `invalid_manifest`, `manifest_incomplete`, `probe_failed`,
`ffmpeg_failed`, `ffmpeg_not_found`, `workspace_failed`。
`internal/tools/usage.md` のリカバリ表はこれらと整合性テストされる。

## テスト戦略

- **ハーメティック** — `go test ./...` は ffmpeg 不要。`master` テストは
  ffprobe に既定尺を返し ffmpeg の出力ファイルを実体化するフェイク `Runner`。
- **純関数** — 引数ビルダとマニフェストパーサを直接テスト。
- **インプロセスツールテスト** — `mcpserver.Server.Call` で名前指定してツールを
  呼ぶので、`tools` テストは stdio フレーミングなしで実ハンドラ経路
  （work_dir・シンボリックリンク拒否含む）を通す。
- **実 ffmpeg** — darwin で手動検証（異なるサイズのスライドを 1920×1080 に
  レター/ピラーボックス、1s + 2s 音声 → 約3秒 MP4）。
