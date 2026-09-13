# ADR-0008: work dir は呼び出しごとの `work_dir`、既定ルートは持たない

- **Status**: Accepted (2026-09-13)

## 背景

組織 ADR-021（ファイル渡し MCP サーバーの work dir 契約）をこのサーバーに適用する。
`master` の `workspace_root` は省略可で、省略すると `~/.video-studio` に書いていた。
そこは呼び出し側のファイルツールが開けない場所で、**mux は成功し、返った MP4 の
パスだけが開けない**という形でしか失敗が出ない。

このサーバーは純粋なコンポジタであり、画像（image-forge）と音声（voice-studio）を
**上流が置いた場所から読む**。上流と同じ `work_dir` を共有することが前提で、
その意味でも「呼び出し側が名指しする 1 つのディレクトリ」に寄せるのが正しい。

## 決定

1. **引数は `work_dir`、`master` で必須。** 意味は「呼び出し側が読み戻せる絶対パス」。
   ワークスペースは `<work_dir>/<workspace_id>/`。
2. **解決順は 引数 → `_meta["jp.nlink/work_dir"]` → エラー。** 既定ルートは持たず、
   `Manager` の既定ルート操作も削除する。
3. **検証は閉じた一覧**（絶対 / `~` 無し / `..` 無し / 存在する dir / 書込可 /
   システム・資格情報の位置でない）。`work_dir_*` の 5 コード。dir は作らない。
4. マニフェスト内の画像・音声パスはワークスペース相対のまま、`os.Root` 封じ込めも変えない。
5. **メディア連鎖は同じ `work_dir` を共有する**（image-forge → voice-studio → ここ）。

## 影響

- **破壊的。** `workspace_root` を送る呼び出しは新しい名前を告げて拒否される
- `~/.video-studio` は使われなくなる（既存の中身はそのまま）
- `internal/workdir` はフリート内の他サーバーと同一ファイル

## 参照

- 組織 ADR-021、voice-scribe ADR-0010（参照実装）、pcap-analyzer-mcp ADR-0008、
  voice-studio-mcp ADR-0013（対になるサーバー）
