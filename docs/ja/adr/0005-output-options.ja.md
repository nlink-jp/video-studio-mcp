# ADR-0005: per-call キャンバス上書きと中間物クリーンアップ

- ステータス: Accepted
- 日付: 2026-07-06

低リスクな出力制御の小改善2件をまとめた wide ADR。

## 背景

- **マルチアスペクト出力。** キャンバスサイズはサーバー `[video]` 設定にあるが、
  エージェントは*同じ*デッキを複数の形で必要とすることが多い —— トーク用 16:9、
  Reels/Shorts/TikTok 用 9:16、フィード用 1:1 —— 1セッション内で。MCP
  クライアントはセッション中にサーバー設定を書き換えにくい。
- **中間物の蓄積。** `master` は `output/tmp` にページセグメント・字幕PNG・concat
  リスト・チャプターメタデータを書くが削除しておらず、レンダのたびに増えていた。

## 決定

- **per-call キャンバス上書き。** `master` に任意 `width`・`height`・`fps` を追加。
  そのレンダに限りサーバー `VideoConfig` のコピーを上書きする。上書き後は新たに
  公開した `config.VideoConfig.Validate()`（偶数・16以上、fps≥1 等）で再検証し、
  不正な上書きは ffmpeg 実行前に `invalid_arguments` で fail fast。画像は既に
  収まるようスケール＋パッドされるので、任意のアスペクト比がそのまま動く。
- **中間物クリーンアップ。** *成功した* concat の後、`master` は `output/tmp` を
  削除する（best-effort。クリーンアップ失敗はレンダを失敗させない）。失敗時は
  デバッグのため tmp を残す。`keep_intermediates: true` でクリーンアップを無効化。

## 帰結

- 1デッキ→複数の納品形をサーバー再設定なしで。レンダ結果は実効
  `width`/`height`/`fps` を反映。
- 既定では `output/` に完成マスターのみ。中間物を検査するテストは
  `keep_intermediates`/`KeepIntermediate` を渡す。
- `VideoConfig.Validate()` が config ロードと per-call 上書きの単一検証経路に
  なり、ルールが両者で乖離しない。
- 新規依存なし。両変更はユニットテスト＋E2E（9:16 レンダが 1080×1920 を報告し
  `output/tmp` が消える）で検証。
