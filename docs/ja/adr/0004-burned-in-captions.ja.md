# ADR-0004: Go 描画オーバーレイによる字幕焼き込み

- ステータス: Accepted
- 日付: 2026-07-06

## 背景

本ツールで字幕が重要なのは、ナレーション音声が日本語であることが多く、動画が
**ミュートの SNS 自動再生**で見られる —— そこではソフト/クローズドキャプション
トラックは既定で非表示 —— からである。当初の RFP は **ffmpeg `drawtext`** ＋
CJK フォント同梱による焼き込みを前提としていた。

環境の実態で方針が変わった: 開発機の ffmpeg は **libfreetype/libass 無し**で
ビルドされており `drawtext` も `subtitles` も存在しない —— ユーザー環境でも前提に
できない。利用可能なのは core の `overlay`・`scale`・`pad`・`concat` 等。

別途、同じ util-series の **json-to-table** が既に **M PLUS 1p**（SIL OFL 1.1,
約1.75MB）を同梱し、`golang.org/x/image/font/opentype`（`opentype.Parse` →
`NewFace` → `font.Drawer`/`MeasureString`）でテキストを PNG 描画している。

## 決定

字幕を ffmpeg でなく **Go で描画**する。焼き込みは `master` のオプトイン
`captions` フラグ（既定 off）とする:

1. `caption` が非空の各ページについて、折り返した字幕テキストを下部中央の
   半透明ボックス付きで、同梱 **M PLUS 1p** フォントを用い `golang.org/x/image`
   で**キャンバスサイズの透明 PNG**に描画（`internal/caption`）。位置決めは
   PNG に焼き込む。
2. ffmpeg core の **`overlay`** で動画に合成: `segmentArgs` は字幕ありページで
   `-filter_complex "[0:v]scale/pad/…[bg];[bg][2:v]overlay=0:0[v]"` ＋明示
   `-map [v] -map 1:a` に切替え、字幕なしなら通常の `-vf` 経路に戻る。

フォントは json-to-table の M PLUS 1p を再利用（実フォント名に修正した
`FONTS_LICENSE`）し、README で OFL 帰属表記する。

## 帰結

- **`overlay`（core フィルタ）を持つ ffmpeg なら動く** —— libfreetype 非依存で
  ユーザー環境間の移植性が高い。これが決め手。
- **レイアウト制御を Go で完結** —— 折り返し（rune 単位・日本語対応）・ボックス・
  色・余白を `internal/caption` で計算し `[caption]` で設定。`drawtext` の
  エスケープ/式と格闘しなくてよい。
- **常時表示** —— 焼き込みピクセルはミュート自動再生やソフト字幕を剥がす
  プラットフォームでも残る。トグル不可・テキスト選択不可というトレードオフは
  対象用途では許容。将来ソフト `mov_text` トラックを補完トグルとして追加可。
- **新規依存** `golang.org/x/image` とバイナリ埋め込み約1.75MB フォント。いずれも
  軽微で、既存の org パターンに一致。
- **ハーメティックテスト** —— 字幕描画は pure Go（PNG デコードしてインク存在を
  検証）、overlay 配線は `segmentArgs` レベル＋実 ffmpeg E2E（出力フレームに白い
  字幕ピクセルを確認）で検証。
- 字幕 PNG は `output/tmp/` 配下の**サーバー書込**なので、エージェント素材に
  適用する spawn 前 `VerifyRegular` チェックなしで ffmpeg に渡す。
